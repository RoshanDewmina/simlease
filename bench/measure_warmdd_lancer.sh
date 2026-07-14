#!/usr/bin/env bash
# bench/measure_warmdd_lancer.sh — P1-7 warm-DD on real Lancer (REVIEW §P1-7).
# Repeated simurgh exec xcodebuild builds of the SAME absolute project path
# (primary checkout — warm-dd keys on path+scheme, not worktree path) so the
# clonefile cache can seed the second lease. Records cold vs warm wall times.
#
# Usage: measure_warmdd_lancer.sh [--reps N] [--out DIR]
# Emits: $OUT/warmdd-lancer.json
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib.sh"

LANCER_REPO="${LANCER_REPO:-$HOME/Documents/command-center}"
SIM_NAME="${BENCH_SIM_NAME:-iPhone 17 Pro}"
OUT_ROOT="$HERE/results"
SIMURGH_BIN="${SIMURGH_BIN:-$HERE/.bin/simurgh}"
REPS=2

usage() { echo "usage: measure_warmdd_lancer.sh [--reps N] [--out DIR]" >&2; exit 2; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --reps) REPS="$2"; shift 2 ;;
    --out) OUT_ROOT="$2"; shift 2 ;;
    -h|--help) usage ;;
    *) echo "measure_warmdd_lancer.sh: unknown flag $1" >&2; usage ;;
  esac
done

RUN_TS="$(date -u +%Y%m%dT%H%M%SZ)"
RESULTS_DIR="$OUT_ROOT/$RUN_TS"
mkdir -p "$RESULTS_DIR"

say() { echo "[$(date -u +%H:%M:%SZ)] $*" | tee -a "$RESULTS_DIR/warmdd.log" >&2; }

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
        BENCH_RAM_FLOOR_GB=0 BENCH_PREFLIGHT_CONFIRM=1 bash "$HERE/preflight.sh" >"$PF_JSON" 2>/dev/null && pf_pass=true
      fi
    fi
  fi
fi

if [[ "$pf_pass" != "true" ]]; then
  say "BLOCKED: preflight failed"
  jq -nc --arg ts "$(iso_ts)" --arg status "blocked" --arg reason "preflight_failed" \
    --slurpfile pf "$PF_JSON" \
    '{timestamp:$ts, check:"warmdd_lancer", status:$status, reason:$reason, preflight:$pf[0], reps:[]}' \
    >"$RESULTS_DIR/warmdd-lancer.json"
  exit 2
fi

if [[ ! -f "$LANCER_REPO/Lancer.xcodeproj/project.pbxproj" ]]; then
  say "generating Lancer.xcodeproj via xcodegen"
  (cd "$LANCER_REPO" && xcodegen generate >/dev/null 2>"$RESULTS_DIR/xcodegen-error.log") || {
    say "xcodegen failed"
    exit 2
  }
fi

PROJECT_PATH="$LANCER_REPO/Lancer.xcodeproj"
SCHEME="Lancer"

if [[ ! -x "$SIMURGH_BIN" ]]; then
  mkdir -p "$(dirname "$SIMURGH_BIN")"
  (cd "$HERE/.." && go build -o "$SIMURGH_BIN" ./cmd/simurgh)
fi

DAEMON_STARTED=0
if ! "$SIMURGH_BIN" status --json >/dev/null 2>&1; then
  nohup "$SIMURGH_BIN" daemon run >"$RESULTS_DIR/daemon.log" 2>&1 &
  echo $! >"$RESULTS_DIR/daemon.pid"
  DAEMON_STARTED=1
  for _ in $(seq 1 30); do
    "$SIMURGH_BIN" status --json >/dev/null 2>&1 && break
    sleep 0.5
  done
fi

stop_daemon() {
  [[ "$DAEMON_STARTED" == "1" && -f "$RESULTS_DIR/daemon.pid" ]] && kill "$(cat "$RESULTS_DIR/daemon.pid")" 2>/dev/null || true
}
trap stop_daemon EXIT

REPS_JSON="[]"
for rep in $(seq 1 "$REPS"); do
  say "rep $rep/$REPS: acquire + build"
  acquire_json="$("$SIMURGH_BIN" acquire --model "$SIM_NAME" --wait --label "bench=warmdd-$RUN_TS" --label "rep=$rep" --json)"
  if [[ $? -ne 0 ]]; then
    say "acquire failed on rep $rep"
    break
  fi
  lease_id="$(echo "$acquire_json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["lease"]["id"])')"
  env_json="$("$SIMURGH_BIN" env "$lease_id" --json)"
  lease_dd="$(echo "$env_json" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("env",{}).get("SIMURGH_DERIVED_DATA",""))')"

  build_log="$RESULTS_DIR/build-rep${rep}.log"
  build_start="$(mono_ns)"
  set +e
  "$SIMURGH_BIN" exec "$lease_id" -- xcodebuild build \
    -project "$PROJECT_PATH" -scheme "$SCHEME" -configuration Debug \
    >"$build_log" 2>&1
  build_exit=$?
  set -e
  build_end="$(mono_ns)"
  build_ns=$((build_end - build_start))

  "$SIMURGH_BIN" release "$lease_id" --json >/dev/null 2>&1 || true

  label="cold"
  [[ "$rep" -gt 1 ]] && label="warm"
  REPS_JSON="$(jq -c --argjson r "$rep" --arg label "$label" --argjson dur "$build_ns" \
    --argjson exit_code "$build_exit" --arg lease_dd "$lease_dd" \
    '. + [{rep:$r, label:$label, build_duration_ns:$dur, build_exit_code:$exit_code, lease_derived_data:$lease_dd}]' <<<"$REPS_JSON")"
  say "rep $rep ($label): exit=$build_exit duration_ns=$build_ns"
  [[ "$build_exit" -ne 0 ]] && break
done

cold_ns="$(echo "$REPS_JSON" | jq -r '.[] | select(.label=="cold") | .build_duration_ns' | head -1)"
warm_med="$(echo "$REPS_JSON" | jq -r '[.[] | select(.label=="warm") | .build_duration_ns] | if length>0 then (add/length|floor) else null end')"

speedup_pct=null
if [[ -n "$cold_ns" && "$cold_ns" != "null" && -n "$warm_med" && "$warm_med" != "null" && "$cold_ns" -gt 0 ]]; then
  speedup_pct="$(python3 -c "print(round((1 - $warm_med/$cold_ns)*100, 1))")"
fi

jq -nc \
  --arg ts "$(iso_ts)" \
  --arg status "complete" \
  --arg project_path "$PROJECT_PATH" \
  --arg scheme "$SCHEME" \
  --argjson reps "$REPS_JSON" \
  --argjson cold_ns "${cold_ns:-null}" \
  --argjson warm_median_ns "${warm_med:-null}" \
  --argjson speedup_pct "${speedup_pct:-null}" \
  --slurpfile pf "$PF_JSON" \
  '{timestamp:$ts, check:"warmdd_lancer", status:$status, project_path:$project_path,
    scheme:$scheme, preflight:$pf[0], reps:$reps,
    cold_build_duration_ns:$cold_ns, warm_build_median_ns:$warm_median_ns,
    warm_speedup_percent:$speedup_pct}' \
  >"$RESULTS_DIR/warmdd-lancer.json"

mkdir -p "$HERE/evidence"
cp "$RESULTS_DIR/warmdd-lancer.json" "$HERE/evidence/warmdd-lancer-${RUN_TS}.json"
say "evidence: $RESULTS_DIR/warmdd-lancer.json (speedup=${speedup_pct}%)"
exit 0
