#!/usr/bin/env bash
# bench/lib.sh — monotonic timing, JSON step emission, incident detectors.
# Sourced by workload.sh, preflight.sh, and the arm_*.sh scripts.
# Constraint (BENCHMARK_PLAN.md §7): bash + jq + python3 stdlib only.

BENCH_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# --- credential scrubbing (evidence / preflight safety) ---------------
#
# Never persist process command lines, raw environ dumps, or credential-
# shaped values into bench JSON. cursor-agent workers put --api-key crsr_*
# on argv; embedding `ps aux` output has already leaked a live key into
# committed evidence once.

# bench_redact_secrets: stdin -> stdout with credential-like values replaced.
# Implementation: bench/redact_secrets.py (mirrors internal/redact.Secrets).
bench_redact_secrets() {
  python3 "$BENCH_LIB_DIR/redact_secrets.py"
}

# --- timing -----------------------------------------------------------

# Monotonic nanosecond timestamp. macOS `date` has no %N; python3's
# time.monotonic_ns() is immune to wall-clock adjustments during a rep.
mono_ns() {
  python3 -c 'import time; print(time.monotonic_ns())'
}

iso_ts() {
  date -u +%Y-%m-%dT%H:%M:%SZ
}

# --- incident detection (BENCHMARK_PLAN.md §2 taxonomy) ---------------
#
# Every pattern below maps to one of the plan's collision incident
# classes: build.db/DerivedData contention, install-over-each-other,
# wrong-variant (evidence integrity), launch/destination failures,
# post-run residue. wrong_variant and install_clobber are detected by
# workload.sh directly (they need cross-step state, not just a log
# grep) — this function covers the log-pattern-detectable classes.

_BENCH_DD_CONTENTION_PATTERNS=(
  'database is locked'
  'Could not create a lock file'
  'unable to attach DB'
  'Failed to load module map'
  'another instance of xcodebuild'
  "couldn't be opened because you don't have permission"
  'Build system information file'
  'lock file.*already'
)

_BENCH_LAUNCH_DESTINATION_PATTERNS=(
  'Unable to find a destination'
  'No devices are booted'
  'does not support'
  'failed to terminate'
  'Failed to install the requested application'
  'Simulator device failed'
  'Invalid device'
  'is not currently.*booted'
)

# bench_classify_log LOGFILE -> JSON array of incidents found in LOGFILE.
# Generic pattern-based classification, used for build/install step logs.
bench_classify_log() {
  local logfile="$1"
  local incidents="[]"
  local pat
  for pat in "${_BENCH_DD_CONTENTION_PATTERNS[@]}"; do
    if grep -qiE -- "$pat" "$logfile" 2>/dev/null; then
      local evidence
      evidence="$(grep -iE -- "$pat" "$logfile" | head -1 | bench_redact_secrets)"
      incidents="$(jq -c --arg t "dd_contention" --arg e "$evidence" \
        '. + [{type:$t, evidence:$e}]' <<<"$incidents")"
    fi
  done
  for pat in "${_BENCH_LAUNCH_DESTINATION_PATTERNS[@]}"; do
    if grep -qiE -- "$pat" "$logfile" 2>/dev/null; then
      local evidence
      evidence="$(grep -iE -- "$pat" "$logfile" | head -1 | bench_redact_secrets)"
      incidents="$(jq -c --arg t "launch_destination_failure" --arg e "$evidence" \
        '. + [{type:$t, evidence:$e}]' <<<"$incidents")"
    fi
  done
  # Overlapping patterns (e.g. "database is locked" and "unable to attach
  # DB" both matching the same line) would otherwise double-count one
  # real incident.
  jq -c 'unique_by([.type, .evidence])' <<<"$incidents"
}

# bench_step NAME -- CMD... -> emits one JSON step record to stdout,
# returns CMD's exit code. Caller redirects/collects the JSON line.
bench_step() {
  local name="$1"; shift
  local logfile
  logfile="$(mktemp)"
  local start end dur exit_code
  start="$(mono_ns)"
  "$@" >"$logfile" 2>&1
  exit_code=$?
  end="$(mono_ns)"
  dur=$(( end - start ))
  local incidents
  incidents="$(bench_classify_log "$logfile")"
  if [[ "$exit_code" -ne 0 && "$incidents" == "[]" ]]; then
    incidents='[{"type":"unclassified_failure","evidence":"nonzero exit, no known pattern matched"}]'
  fi
  local log_snip
  log_snip="$(tail -c 4000 "$logfile" | bench_redact_secrets)"
  jq -nc \
    --arg name "$name" \
    --argjson start_ns "$start" \
    --argjson end_ns "$end" \
    --argjson duration_ns "$dur" \
    --argjson exit_code "$exit_code" \
    --argjson incidents "$incidents" \
    --arg log "$log_snip" \
    '{step:$name, start_ns:$start_ns, end_ns:$end_ns, duration_ns:$duration_ns, exit_code:$exit_code, incidents:$incidents, log:$log}'
  rm -f "$logfile"
  return "$exit_code"
}

# bench_residue_check PREFIX -> JSON describing leftover simulators and
# scratch dirs whose name/path starts with PREFIX (e.g. "bench-"). Used
# between reps (plan step 5: "no simurgh-clones, no stray daemons,
# DerivedData state per arm semantics") and by preflight check 3.
bench_residue_check() {
  local prefix="$1"
  local devices
  devices="$(xcrun simctl list devices -j 2>/dev/null | python3 -c '
import json, sys
data = json.load(sys.stdin)
prefix = sys.argv[1]
out = []
for runtime, devs in data.get("devices", {}).items():
    for d in devs:
        if d.get("name", "").startswith(prefix):
            out.append({"name": d["name"], "udid": d.get("udid", ""), "state": d.get("state", "")})
print(json.dumps(out))
' "$prefix")"
  local n
  n="$(jq 'length' <<<"$devices")"
  jq -nc --argjson devices "$devices" --argjson count "$n" \
    '{prefix_devices: $devices, count: $count}'
}

# bench_hash_file PATH -> sha256 or "" if PATH doesn't exist. Used by
# workload.sh's install-over-each-other (bundle replaced mid-run) check.
bench_hash_file() {
  local path="$1"
  if [[ -f "$path" ]]; then
    shasum -a 256 "$path" | awk '{print $1}'
  else
    echo ""
  fi
}
