#!/usr/bin/env bash
# bench/check_xcodebuildmcp.sh — B6 XcodeBuildMCP composition check
# (BENCHMARK_PLAN.md §2, REVIEW_AND_NEXT_STEPS.md P0-3). Inside an active
# simurgh lease, drive a Lancer simulator build through XcodeBuildMCP CLI
# with the lease's isolated DerivedData path + simulator UDID, then verify
# artifacts land under the lease path (not the global DerivedData tree).
#
# Usage: check_xcodebuildmcp.sh [--out DIR] [--dry-run]
# Emits: $OUT/xcodebuildmcp-composition.json (+ preflight.json if run)
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib.sh"

LANCER_REPO="${LANCER_REPO:-$HOME/Documents/command-center}"
SIM_NAME="${BENCH_SIM_NAME:-iPhone 17 Pro}"
XBMCP_VERSION="${XBMCP_VERSION:-2.6.2}"
OUT_ROOT="$HERE/results"
SIMURGH_BIN="${SIMURGH_BIN:-$HERE/.bin/simurgh}"
DRY_RUN=0

usage() { echo "usage: check_xcodebuildmcp.sh [--out DIR] [--dry-run]" >&2; exit 2; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --out) OUT_ROOT="$2"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) usage ;;
    *) echo "check_xcodebuildmcp.sh: unknown flag $1" >&2; usage ;;
  esac
done

RUN_TS="$(date -u +%Y%m%dT%H%M%SZ)"
RESULTS_DIR="$OUT_ROOT/$RUN_TS"
mkdir -p "$RESULTS_DIR"

if [[ "$DRY_RUN" == "1" ]]; then
  jq -nc \
    --arg ts "$(iso_ts)" \
    --arg status "blocked" \
    --arg reason "dry_run_offline" \
    '{timestamp:$ts, check:"xcodebuildmcp_composition", status:$status,
      reason:$reason, verdict:null,
      note:"offline harness self-test — run without --dry-run on a quiet machine with Lancer checkout"}' \
    >"$RESULTS_DIR/xcodebuildmcp-composition.json"
  echo "check_xcodebuildmcp.sh: dry-run wrote $RESULTS_DIR/xcodebuildmcp-composition.json" >&2
  exit 0
fi

say() { echo "[$(date -u +%H:%M:%SZ)] $*" | tee -a "$RESULTS_DIR/check.log" >&2; }

# --- preflight (recorded; dormant-stray exception matches driver) ------
PF_JSON="$RESULTS_DIR/preflight.json"
pf_pass=false
if BENCH_RAM_FLOOR_GB=0 bash "$HERE/preflight.sh" >"$PF_JSON" 2>/dev/null; then
  pf_pass=true
else
  fails="$(python3 -c 'import json;print(",".join(c["name"] for c in json.load(open("'"$PF_JSON"'"))["checks"] if not c["ok"]))' 2>/dev/null || echo "unknown")"
  if [[ "$fails" == "stray_processes" ]]; then
    pids="$(pgrep -f 'cursor-agent-worker|cursor-agent$|cursor-agent ' || true)"
    if [[ -n "$pids" ]]; then
      t1="$(ps -o cputime= -p $pids 2>/dev/null | awk -F'[:.]' '{s+=$1*60+$2} END {print s+0}')"
      sleep 60
      t2="$(ps -o cputime= -p $pids 2>/dev/null | awk -F'[:.]' '{s+=$1*60+$2} END {print s+0}')"
      delta=$(( ${t2:-0} - ${t1:-0} ))
      jq -nc --arg pids "$(echo $pids | tr '\n' ' ')" --argjson d "$delta" \
        '{stray_pids:$pids, cpu_seconds_over_60s:$d}' >"$RESULTS_DIR/stray-dormancy.json"
      if [[ "$delta" -lt 1 ]]; then
        say "preflight: dormant-stray exception (cpu_delta=${delta}s)"
        BENCH_RAM_FLOOR_GB=0 BENCH_PREFLIGHT_CONFIRM=1 bash "$HERE/preflight.sh" >"$PF_JSON" 2>/dev/null && pf_pass=true
      fi
    fi
  fi
fi

if [[ "$pf_pass" != "true" ]]; then
  # Never dump full preflight JSON into logs — messages historically embedded
  # process argv (and thus --api-key). Names of failing checks only.
  fails="$(python3 -c 'import json;print(",".join(c["name"] for c in json.load(open("'"$PF_JSON"'"))["checks"] if not c["ok"]))' 2>/dev/null || echo "unknown")"
  say "BLOCKED: preflight failed — checks: ${fails}"
  jq -nc \
    --arg ts "$(iso_ts)" \
    --arg status "blocked" \
    --arg reason "preflight_failed" \
    --slurpfile pf "$PF_JSON" \
    '{timestamp:$ts, check:"xcodebuildmcp_composition", status:$status, reason:$reason, preflight:$pf[0], verdict:null}' \
    >"$RESULTS_DIR/xcodebuildmcp-composition.json"
  exit 2
fi

if [[ ! -d "$LANCER_REPO/.git" ]]; then
  say "BLOCKED: Lancer repo missing at $LANCER_REPO"
  exit 2
fi

# --- build simurgh + start daemon --------------------------------------
if [[ ! -x "$SIMURGH_BIN" ]]; then
  say "building simurgh -> $SIMURGH_BIN"
  mkdir -p "$(dirname "$SIMURGH_BIN")"
  (cd "$HERE/.." && go build -o "$SIMURGH_BIN" ./cmd/simurgh)
fi

DAEMON_STARTED=0
if ! "$SIMURGH_BIN" status --json >/dev/null 2>&1; then
  say "starting simurgh daemon"
  nohup "$SIMURGH_BIN" daemon run >"$RESULTS_DIR/daemon.log" 2>&1 &
  echo $! >"$RESULTS_DIR/daemon.pid"
  DAEMON_STARTED=1
  for _ in $(seq 1 30); do
    "$SIMURGH_BIN" status --json >/dev/null 2>&1 && break
    sleep 0.5
  done
fi

cleanup() {
  [[ -n "${LEASE_ID:-}" ]] && "$SIMURGH_BIN" release "$LEASE_ID" --json >/dev/null 2>&1 || true
  [[ "$DAEMON_STARTED" == "1" && -f "$RESULTS_DIR/daemon.pid" ]] && kill "$(cat "$RESULTS_DIR/daemon.pid")" 2>/dev/null || true
  [[ -n "${WORKTREE:-}" && -d "$WORKTREE" ]] && git -C "$LANCER_REPO" worktree remove --force "$WORKTREE" >/dev/null 2>&1 || rm -rf "${WORKTREE:-}"
  git -C "$LANCER_REPO" worktree prune >/dev/null 2>&1 || true
}
trap cleanup EXIT

MASTER_COMMIT="$(git -C "$LANCER_REPO" rev-parse master)"
WORKTREE="$RESULTS_DIR/wt-xbmcp-$RUN_TS"
git -C "$LANCER_REPO" worktree add --detach "$WORKTREE" "$MASTER_COMMIT" >/dev/null
if ! (cd "$WORKTREE" && xcodegen generate >/dev/null 2>"$WORKTREE/.xcodegen-error.log"); then
  say "xcodegen failed: $(cat "$WORKTREE/.xcodegen-error.log" 2>/dev/null)"
  jq -nc --arg ts "$(iso_ts)" --arg status "blocked" --arg reason "xcodegen_failed" \
    '{timestamp:$ts, check:"xcodebuildmcp_composition", status:$status, reason:$reason, verdict:null}' \
    >"$RESULTS_DIR/xcodebuildmcp-composition.json"
  exit 2
fi

say "acquiring lease (model=$SIM_NAME)"
acquire_json="$("$SIMURGH_BIN" acquire --model "$SIM_NAME" --wait --label "bench=xbmcp-$RUN_TS" --json)"
acquire_exit=$?
if [[ $acquire_exit -ne 0 ]]; then
  say "acquire failed: $acquire_json"
  exit 2
fi
LEASE_ID="$(echo "$acquire_json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["lease"]["id"])')"
UDID="$(echo "$acquire_json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["lease"]["device"]["udid"])')"
ENV_JSON="$("$SIMURGH_BIN" env "$LEASE_ID" --json)"
LEASE_DD="$(echo "$ENV_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("env",{}).get("SIMURGH_DERIVED_DATA",""))')"

if [[ -z "$LEASE_DD" ]]; then
  say "lease env missing SIMURGH_DERIVED_DATA"
  exit 2
fi

GLOBAL_DD="$HOME/Library/Developer/Xcode/DerivedData"
before_global="$(find "$GLOBAL_DD" -maxdepth 1 -name 'Lancer-*' -newer "$PF_JSON" 2>/dev/null | wc -l | tr -d ' ')"

say "XcodeBuildMCP build (v$XBMCP_VERSION, dd=$LEASE_DD, udid=$UDID)"
build_start="$(mono_ns)"
build_log="$RESULTS_DIR/xbmcp-build.log"
set +e
npx --yes "xcodebuildmcp@${XBMCP_VERSION}" simulator build \
  --project-path "$WORKTREE/Lancer.xcodeproj" \
  --scheme Lancer \
  --configuration Debug \
  --simulator-id "$UDID" \
  --derived-data-path "$LEASE_DD" \
  --output json >"$RESULTS_DIR/xbmcp-build.json" 2>"$build_log"
build_exit=$?
set -e
build_end="$(mono_ns)"

after_global="$(find "$GLOBAL_DD" -maxdepth 1 -name 'Lancer-*' -newer "$PF_JSON" 2>/dev/null | wc -l | tr -d ' ')"
dd_populated=false
if [[ -d "$LEASE_DD" ]]; then
  dd_entries="$(find "$LEASE_DD" -mindepth 1 -maxdepth 2 2>/dev/null | wc -l | tr -d ' ')"
  [[ "${dd_entries:-0}" -gt 0 ]] && dd_populated=true
fi

dd_under_lease=$([[ "$dd_populated" == "true" ]] && echo true || echo false)
no_global_pollution=$([[ "${after_global:-0}" -le "${before_global:-0}" ]] && echo true || echo false)
build_ok=$([[ "$build_exit" -eq 0 ]] && echo true || echo false)

verdict="$(python3 -c '
import sys
sys.path.insert(0, sys.argv[1])
from xcodebuildmcp_composition import composition_verdict
print(composition_verdict(
    build_ok=sys.argv[2] == "true",
    dd_under_lease=sys.argv[3] == "true",
    no_global_pollution=sys.argv[4] == "true",
))
' "$HERE" "$build_ok" "$dd_under_lease" "$no_global_pollution")"

say "verdict=$verdict build_exit=$build_exit dd_under_lease=$dd_under_lease no_global_pollution=$no_global_pollution"

jq -nc \
  --arg ts "$(iso_ts)" \
  --arg status "complete" \
  --arg verdict "$verdict" \
  --argjson build_exit "$build_exit" \
  --argjson build_duration_ns "$((build_end - build_start))" \
  --arg lease_id "$LEASE_ID" \
  --arg udid "$UDID" \
  --arg lease_dd "$LEASE_DD" \
  --argjson dd_under_lease "$dd_under_lease" \
  --argjson no_global_pollution "$no_global_pollution" \
  --arg xbmcp_version "$XBMCP_VERSION" \
  --arg worktree "$WORKTREE" \
  --arg lancer_commit "$MASTER_COMMIT" \
  --slurpfile pf "$PF_JSON" \
  --slurpfile env "$ENV_JSON" \
  '{timestamp:$ts, check:"xcodebuildmcp_composition", status:$status, verdict:$verdict,
    xcodebuildmcp_version:$xbmcp_version, build_exit_code:$build_exit,
    build_duration_ns:$build_duration_ns, lease_id:$lease_id, udid:$udid,
    lease_derived_data:$lease_dd, dd_populated_under_lease:$dd_under_lease,
    no_new_global_lancer_dd:$no_global_pollution, lancer_commit:$lancer_commit,
    worktree:$worktree, preflight:$pf[0], lease_env:$env[0]}' \
  >"$RESULTS_DIR/xcodebuildmcp-composition.json"

cp "$RESULTS_DIR/xcodebuildmcp-composition.json" "$HERE/evidence/xcodebuildmcp-composition-${RUN_TS}.json" 2>/dev/null || \
  mkdir -p "$HERE/evidence" && cp "$RESULTS_DIR/xcodebuildmcp-composition.json" "$HERE/evidence/xcodebuildmcp-composition-${RUN_TS}.json"

say "evidence: $RESULTS_DIR/xcodebuildmcp-composition.json"
[[ "$verdict" == "composes" ]] && exit 0 || exit 1
