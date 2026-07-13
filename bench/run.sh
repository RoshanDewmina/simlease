#!/usr/bin/env bash
# bench/run.sh — matrix driver (BENCHMARK_PLAN.md §7).
#
# Usage:
#   run.sh --scenario B1 [--reps N] [--arms N,D,S] [--out DIR]
#
# Drives one scenario across the requested arms/reps, writing one JSON
# per agent-task to results/<ts>/run-<scenario>-<arm>-rep<rep>-<agent>.json,
# then generates summary.md via report.py.
#
# Scenario table (BENCHMARK_PLAN.md §2). B6 (real-world Lancer) is a
# separate real-repo workflow, not driven by this generic toy-fixture
# runner — see HANDOFF_BENCH_PROMPT.md step 5 for its distinct rules.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib.sh"

SCENARIO=""
REPS=""
ARMS=""
OUT_ROOT="$HERE/results"

usage() {
  echo "usage: run.sh --scenario B1..B5 [--reps N] [--arms N,D,S] [--out DIR]" >&2
  exit 2
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --scenario) SCENARIO="$2"; shift 2 ;;
    --reps) REPS="$2"; shift 2 ;;
    --arms) ARMS="$2"; shift 2 ;;
    --out) OUT_ROOT="$2"; shift 2 ;;
    -h|--help) usage ;;
    *) echo "run.sh: unknown flag $1" >&2; usage ;;
  esac
done
[[ -z "$SCENARIO" ]] && usage

# --- scenario table: agents, default arms, default reps ---------------
case "$SCENARIO" in
  B1) DEFAULT_AGENTS=2; DEFAULT_ARMS="N,D,S"; DEFAULT_REPS=10 ;;
  B2) DEFAULT_AGENTS=4; DEFAULT_ARMS="N,D,S"; DEFAULT_REPS=5 ;;
  B3) DEFAULT_AGENTS=1; DEFAULT_ARMS="N,S";   DEFAULT_REPS=10 ;;
  B4) DEFAULT_AGENTS=2; DEFAULT_ARMS="N,D,S"; DEFAULT_REPS=5 ;;
  B5) DEFAULT_AGENTS=6; DEFAULT_ARMS="N,S";   DEFAULT_REPS=3 ;;
  *) echo "run.sh: unknown scenario '$SCENARIO' (expected B1..B5; B6 is a separate real-repo workflow)" >&2; exit 2 ;;
esac
REPS="${REPS:-$DEFAULT_REPS}"
ARMS="${ARMS:-$DEFAULT_ARMS}"
AGENT_LETTERS=(A B C D E F)
AGENTS=("${AGENT_LETTERS[@]:0:$DEFAULT_AGENTS}")

RUN_TS="$(date -u +%Y%m%dT%H%M%SZ)"
RUN_ID="${SCENARIO}-${RUN_TS}"
RESULTS_DIR="$OUT_ROOT/$RUN_TS"
mkdir -p "$RESULTS_DIR"

SIMURGH_BIN="${SIMURGH_BIN:-$HERE/.bin/simurgh}"
DAEMON_STARTED_BY_US=0

ensure_daemon() {
  if [[ ! -x "$SIMURGH_BIN" ]]; then
    echo "run.sh: building simurgh CLI to $SIMURGH_BIN" >&2
    mkdir -p "$(dirname "$SIMURGH_BIN")"
    (cd "$HERE/.." && go build -o "$SIMURGH_BIN" ./cmd/simurgh)
  fi
  if ! "$SIMURGH_BIN" status --json >/dev/null 2>&1; then
    echo "run.sh: starting simurgh daemon" >&2
    nohup "$SIMURGH_BIN" daemon run >"$RESULTS_DIR/daemon.log" 2>&1 &
    echo $! >"$RESULTS_DIR/daemon.pid"
    DAEMON_STARTED_BY_US=1
    for _ in $(seq 1 20); do
      "$SIMURGH_BIN" status --json >/dev/null 2>&1 && break
      sleep 0.5
    done
  fi
}

stop_daemon_if_ours() {
  if [[ "$DAEMON_STARTED_BY_US" == "1" && -f "$RESULTS_DIR/daemon.pid" ]]; then
    kill "$(cat "$RESULTS_DIR/daemon.pid")" 2>/dev/null || true
  fi
}
trap stop_daemon_if_ours EXIT

run_rep() {
  local arm_code="$1" rep="$2"
  local arm_name arm_script
  case "$arm_code" in
    N) arm_name="naive"; arm_script="$HERE/arm_naive.sh" ;;
    D) arm_name="diy"; arm_script="$HERE/arm_diy.sh" ;;
    S) arm_name="simurgh"; arm_script="$HERE/arm_simurgh.sh"; ensure_daemon ;;
    *) echo "run.sh: unknown arm code '$arm_code'" >&2; return 2 ;;
  esac

  echo "== $SCENARIO arm=$arm_name rep=$rep agents=${AGENTS[*]} ==" >&2
  local pids=()
  local outs=()
  for agent in "${AGENTS[@]}"; do
    local out="$RESULTS_DIR/run-${SCENARIO}-${arm_name}-rep${rep}-${agent}.json"
    outs+=("$out")
    (
      export BENCH_AGENT="$agent"
      export BENCH_SCENARIO="$SCENARIO"
      export BENCH_REP="$rep"
      export BENCH_OUT="$out"
      export BENCH_RUN_ID="$RUN_ID"
      export SIMURGH_BIN
      "$arm_script"
    ) &
    pids+=($!)
  done

  local rep_exit=0
  for pid in "${pids[@]}"; do
    wait "$pid" || rep_exit=1
  done

  # Between-reps cleanup verification (HANDOFF_BENCH_PROMPT.md step 5):
  # measured, not enforced — ARM-D residue is the expected DIY cost;
  # ARM-S residue would itself be an incident.
  local residue
  residue="$(bench_residue_check "simurgh-clone")"
  if [[ "$arm_name" == "simurgh" ]]; then
    local rc
    rc="$(jq '.count' <<<"$residue")"
    if [[ "$rc" -gt 0 ]]; then
      echo "WARNING: $rc simurgh-clone device(s) left after ARM-S rep $rep — this is itself an incident" >&2
    fi
  fi
  jq -nc --argjson residue "$residue" --arg arm "$arm_name" --argjson rep "$rep" \
    '{arm:$arm, rep:$rep, post_rep_residue:$residue}' >>"$RESULTS_DIR/residue-log.jsonl"

  return $rep_exit
}

overall_exit=0
IFS=',' read -ra ARM_LIST <<<"$ARMS"
for arm_code in "${ARM_LIST[@]}"; do
  for rep in $(seq 1 "$REPS"); do
    run_rep "$arm_code" "$rep" || overall_exit=1
  done
done

echo "run.sh: results in $RESULTS_DIR" >&2
python3 "$HERE/report.py" "$RESULTS_DIR" >"$RESULTS_DIR/summary.md"
echo "run.sh: summary at $RESULTS_DIR/summary.md" >&2

exit $overall_exit
