#!/usr/bin/env bash
# bench/preflight.sh — BENCHMARK_PLAN.md §4 quiet-machine gate.
# Must pass before any timed rep counts (ground rule 4). Never kills
# anything itself — a stray-process finding blocks the run until the
# owner re-invokes with BENCH_PREFLIGHT_CONFIRM=1 (plan: "ask the owner
# before killing anything that isn't yours").
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib.sh"

REPO_ROOT="$(cd "$HERE/.." && pwd)"
FAILURES=()
CHECKS="[]"

add_check() {
  local name="$1" ok="$2" message="$3"
  CHECKS="$(jq -c --arg n "$name" --argjson ok "$ok" --arg m "$message" \
    '. + [{name:$n, ok:$ok, message:$m}]' <<<"$CHECKS")"
  if [[ "$ok" != "true" ]]; then
    FAILURES+=("$name: $message")
  fi
}

# 1. No stray cursor-agent-worker processes (docs/plans/orchestrator-state.md
#    2026-07-13 M4 entry: these are the specific processes that have
#    previously held this Mac below the RAM pressure floor). Matched on
#    the literal binary name only — NOT a broad "claude|worker" pattern,
#    which false-positives on this very session's own Claude Desktop
#    renderer helper (its Chromium flags happen to contain the substring
#    "worker" via --service-worker-schemes).
stray="$(ps aux | grep -E 'cursor-agent-worker|cursor-agent$|cursor-agent ' | grep -v grep || true)"
if [[ -n "$stray" ]]; then
  if [[ "${BENCH_PREFLIGHT_CONFIRM:-0}" == "1" ]]; then
    add_check "stray_processes" true "found stray processes but BENCH_PREFLIGHT_CONFIRM=1 (owner acknowledged): $stray"
  else
    add_check "stray_processes" false "stray agent processes found — owner must confirm via BENCH_PREFLIGHT_CONFIRM=1 or clear them: $stray"
  fi
else
  add_check "stray_processes" true "none found"
fi

# 2. Free+inactive RAM >= floor (default 8GB, BENCHMARK_PLAN.md §4).
#    BENCH_RAM_FLOOR_GB=0 disables the gate (still records the real
#    number for provenance) — macOS pages to swap/compressor under
#    pressure rather than failing outright, so a hard absolute floor
#    tuned for one machine's total RAM doesn't transfer to a smaller
#    box (e.g. GitHub's standard macOS runner has 7GB total RAM,
#    physically incapable of ever showing 8GB free+inactive). Owner
#    call: disabled for CI, left at the plan's default locally.
RAM_FLOOR_GB="${BENCH_RAM_FLOOR_GB:-8}"
vmstat="$(vm_stat)"
page_size="$(echo "$vmstat" | head -1 | grep -oE '[0-9]+' | head -1)"
free_pages="$(echo "$vmstat" | awk -F'[: .]+' '/Pages free/ {print $3}')"
inactive_pages="$(echo "$vmstat" | awk -F'[: .]+' '/Pages inactive/ {print $3}')"
free_inactive_bytes=$(( (free_pages + inactive_pages) * page_size ))
free_inactive_gb=$(python3 -c "print(round($free_inactive_bytes / 1024**3, 2))")
if [[ "$RAM_FLOOR_GB" == "0" ]]; then
  add_check "ram_pressure" true "free+inactive = ${free_inactive_gb}GB (floor disabled via BENCH_RAM_FLOOR_GB=0)"
else
  ram_ok=$(python3 -c "print('true' if $free_inactive_bytes >= $RAM_FLOOR_GB*1024**3 else 'false')")
  add_check "ram_pressure" "$ram_ok" "free+inactive = ${free_inactive_gb}GB (floor: ${RAM_FLOOR_GB}GB)"
fi

# 3. No simurgh-clone or bench-* leftover simulators.
residue_simurgh="$(bench_residue_check "simurgh-clone")"
residue_bench="$(bench_residue_check "bench-")"
residue_count=$(( $(jq '.count' <<<"$residue_simurgh") + $(jq '.count' <<<"$residue_bench") ))
residue_ok=$([[ $residue_count -eq 0 ]] && echo true || echo false)
add_check "simulator_residue" "$residue_ok" "$(jq -nc --argjson a "$residue_simurgh" --argjson b "$residue_bench" '{simurgh_clone:$a, bench:$b}')"

# 4. Xcode + runtime versions (provenance — recorded, not gated: version
#    drift shouldn't block a run, only be reported so results carry it).
xcode_version="$(xcodebuild -version 2>&1 | tr '\n' ' ')"
runtime_list="$(xcrun simctl list runtimes -j 2>/dev/null | python3 -c 'import json,sys; d=json.load(sys.stdin); print(", ".join(r["name"] for r in d.get("runtimes", []) if r.get("isAvailable")))' 2>/dev/null || echo "unknown")"
add_check "xcode_provenance" true "$xcode_version | runtimes: $runtime_list"

# 5. Screen lock / idle — best-effort via HID idle time (seconds since
#    last input). Advisory only: cannot force the owner to lock the
#    screen, and login-window sessions report no HIDIdleTime entry.
idle_ns="$(ioreg -c IOHIDSystem 2>/dev/null | awk '/HIDIdleTime/ {print $NF; exit}')"
if [[ -n "${idle_ns:-}" ]]; then
  idle_s=$(python3 -c "print(round($idle_ns / 1e9, 1))")
  add_check "idle_time" true "${idle_s}s since last input (advisory — plan requires no user activity during timed reps)"
else
  add_check "idle_time" true "could not read HIDIdleTime (advisory, non-blocking)"
fi

RESULT_JSON="$(jq -nc --argjson checks "$CHECKS" --arg ts "$(iso_ts)" \
  --argjson pass "$([[ ${#FAILURES[@]} -eq 0 ]] && echo true || echo false)" \
  '{timestamp:$ts, pass:$pass, checks:$checks}')"

echo "$RESULT_JSON"

if [[ ${#FAILURES[@]} -gt 0 ]]; then
  echo "PREFLIGHT FAILED:" >&2
  for f in "${FAILURES[@]}"; do echo "  - $f" >&2; done
  exit 1
fi
exit 0
