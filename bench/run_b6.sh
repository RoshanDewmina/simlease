#!/usr/bin/env bash
# bench/run_b6.sh — B6: real-world Lancer (BENCHMARK_PLAN.md §2, B6
# section). The original problem, reproduced: each agent works in its
# own disposable git worktree of ~/Documents/command-center, checked
# out at master's last COMMIT (never the primary checkout's working
# tree, which may itself have uncommitted changes — those are never
# touched). Build/simulator-install/launch/screenshot only: no test
# targets (LancerUITests includes relay/pairing flows the project's
# own docs flag as a single-slot scarce resource), no pushes, no
# mutation of Lancer repo state. Worktrees are removed after every rep.
#
# Usage: run_b6.sh [--reps N] [--arms N,D,S] [--agents 2|3]
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib.sh"

LANCER_REPO="${LANCER_REPO:-$HOME/Documents/command-center}"
SIM_NAME="${BENCH_SIM_NAME:-iPhone 17 Pro}"
OUT_ROOT="$HERE/results"

REPS=3
ARMS="N,D,S"
AGENT_COUNT=2

usage() { echo "usage: run_b6.sh [--reps N] [--arms N,D,S] [--agents 2|3] [--out DIR]" >&2; exit 2; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --reps) REPS="$2"; shift 2 ;;
    --arms) ARMS="$2"; shift 2 ;;
    --agents) AGENT_COUNT="$2"; shift 2 ;;
    --out) OUT_ROOT="$2"; shift 2 ;;
    -h|--help) usage ;;
    *) echo "run_b6.sh: unknown flag $1" >&2; usage ;;
  esac
done

if [[ ! -d "$LANCER_REPO/.git" ]]; then
  echo "run_b6.sh: $LANCER_REPO is not a git repo — B6 needs the real command-center checkout" >&2
  exit 1
fi
MASTER_COMMIT="$(git -C "$LANCER_REPO" rev-parse master)"
echo "run_b6.sh: worktrees check out master @ $MASTER_COMMIT (detached) — primary checkout's working tree is never touched, never pushed" >&2

AGENT_LETTERS=(A B C)
AGENTS=("${AGENT_LETTERS[@]:0:$AGENT_COUNT}")

RUN_TS="$(date -u +%Y%m%dT%H%M%SZ)"
RUN_ID="B6-${RUN_TS}"
RESULTS_DIR="$OUT_ROOT/$RUN_TS"
mkdir -p "$RESULTS_DIR"

SIMURGH_BIN="${SIMURGH_BIN:-$HERE/.bin/simurgh}"
DAEMON_STARTED_BY_US=0

ensure_daemon() {
  if [[ ! -x "$SIMURGH_BIN" ]]; then
    echo "run_b6.sh: building simurgh CLI to $SIMURGH_BIN" >&2
    mkdir -p "$(dirname "$SIMURGH_BIN")"
    (cd "$HERE/.." && go build -o "$SIMURGH_BIN" ./cmd/simurgh)
  fi
  if ! "$SIMURGH_BIN" status --json >/dev/null 2>&1; then
    echo "run_b6.sh: starting simurgh daemon" >&2
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

WORKTREES_THIS_REP=()
add_worktree() {
  local path="$1"
  git -C "$LANCER_REPO" worktree add --detach "$path" "$MASTER_COMMIT" >/dev/null 2>&1
  WORKTREES_THIS_REP+=("$path")
  # Lancer.xcodeproj is gitignored (*.xcodeproj/ — command-center's own
  # .gitignore) and generated on demand via xcodegen, same pattern as
  # our own SampleApp fixture. A fresh worktree only has committed
  # files, so it never has an .xcodeproj until this runs.
  if ! (cd "$path" && xcodegen generate >/dev/null 2>"$path/.xcodegen-error.log"); then
    echo "run_b6.sh: xcodegen generate failed in $path" >&2
    cat "$path/.xcodegen-error.log" >&2
  fi
}
remove_rep_worktrees() {
  local wt
  for wt in "${WORKTREES_THIS_REP[@]}"; do
    git -C "$LANCER_REPO" worktree remove --force "$wt" >/dev/null 2>&1 || rm -rf "$wt"
  done
  WORKTREES_THIS_REP=()
  git -C "$LANCER_REPO" worktree prune >/dev/null 2>&1 || true
}

resolve_shared_udid() {
  xcrun simctl list devices -j 2>/dev/null | python3 -c '
import json, sys
data = json.load(sys.stdin)
name = sys.argv[1]
for runtime, devs in data.get("devices", {}).items():
    for d in devs:
        if d.get("name") == name and d.get("isAvailable", True):
            print(d["udid"]); sys.exit(0)
sys.exit(1)
' "$SIM_NAME"
}

trap 'remove_rep_worktrees; stop_daemon_if_ours' EXIT

run_rep_b6() {
  local arm_code="$1" rep="$2"
  local arm_name
  case "$arm_code" in
    N) arm_name="naive" ;;
    D) arm_name="diy" ;;
    S) arm_name="simurgh"; ensure_daemon ;;
    *) echo "run_b6.sh: unknown arm code '$arm_code'" >&2; return 2 ;;
  esac

  echo "== B6 arm=$arm_name rep=$rep agents=${AGENTS[*]} (real Lancer build) ==" >&2

  local shared_udid=""
  [[ "$arm_name" == "naive" ]] && shared_udid="$(resolve_shared_udid)"

  local pids=()
  for agent in "${AGENTS[@]}"; do
    local worktree="$RESULTS_DIR/wt-${arm_name}-rep${rep}-${agent}"
    add_worktree "$worktree"
    local out="$RESULTS_DIR/run-B6-${arm_name}-rep${rep}-${agent}.json"
    (
      export BENCH_AGENT="$agent"
      export BENCH_SCENARIO="B6"
      export BENCH_REP="$rep"
      export BENCH_OUT="$out"
      export BENCH_WORKTREE="$worktree"
      export BENCH_RUN_ID="$RUN_ID"
      export SIMURGH_BIN

      # Each branch only sets up its isolation env (and a cleanup trap
      # for whatever it provisioned) — the actual workload call and
      # exit code are unconditional and shared below, not duplicated
      # per branch.
      case "$arm_name" in
        naive)
          export BENCH_ARM="naive"
          export BENCH_UDID="$shared_udid"
          export BENCH_DESTINATION="platform=iOS Simulator,name=$SIM_NAME"
          export BENCH_DD="" BENCH_SWIFTPM="" BENCH_BUILD_PREFIX=""
          ;;
        diy)
          devtype="com.apple.CoreSimulator.SimDeviceType.iPhone-17-Pro"
          runtime="$(xcrun simctl list runtimes -j 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
for r in d.get("runtimes", []):
    if r.get("isAvailable") and r.get("name","").startswith("iOS"):
        print(r["identifier"]); sys.exit(0)
')"
          device_name="bench-diy-b6-${agent}-${RUN_ID}"
          udid="$(xcrun simctl create "$device_name" "$devtype" "$runtime")"
          xcrun simctl boot "$udid" >/dev/null 2>&1
          xcrun simctl bootstatus "$udid" -b >/dev/null 2>&1 || true
          scratch="$HERE/results/.scratch/b6-diy/${agent}-${RUN_ID}"
          mkdir -p "$scratch/DerivedData" "$scratch/SwiftPM"
          trap 'xcrun simctl shutdown "'"$udid"'" >/dev/null 2>&1 || true' EXIT
          export BENCH_ARM="diy"
          export BENCH_UDID="$udid"
          export BENCH_DESTINATION="platform=iOS Simulator,id=$udid"
          export BENCH_DD="$scratch/DerivedData"
          export BENCH_SWIFTPM="$scratch/SwiftPM"
          export BENCH_BUILD_PREFIX=""
          ;;
        simurgh)
          acquire_json="$("$SIMURGH_BIN" acquire --model "$SIM_NAME" --wait --label "bench=$RUN_ID" --label "agent=$agent" --json)"
          acquire_exit=$?
          if [[ $acquire_exit -ne 0 ]]; then
            echo "run_b6.sh: acquire failed for agent $agent: $acquire_json" >&2
            exit 1
          fi
          lease_id="$(echo "$acquire_json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["lease"]["id"])')"
          udid="$(echo "$acquire_json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["lease"]["device"]["udid"])')"
          trap '"'"$SIMURGH_BIN"'" release "'"$lease_id"'" --json >/dev/null 2>&1 || true' EXIT
          export BENCH_ARM="simurgh"
          export BENCH_UDID="$udid"
          export BENCH_DD="" BENCH_SWIFTPM=""
          export BENCH_BUILD_PREFIX="$SIMURGH_BIN exec $lease_id --"
          ;;
      esac

      "$HERE/workload_lancer.sh"
      exit $?
    ) &
    pids+=($!)
  done

  local rep_exit=0
  for pid in "${pids[@]}"; do
    wait "$pid" || rep_exit=1
  done

  local residue
  residue="$(bench_residue_check "simurgh-clone")"
  jq -nc --argjson residue "$residue" --arg arm "$arm_name" --argjson rep "$rep" \
    '{arm:$arm, rep:$rep, post_rep_residue:$residue}' >>"$RESULTS_DIR/b6-residue-log.jsonl"

  remove_rep_worktrees

  return $rep_exit
}

overall_exit=0
IFS=',' read -ra ARM_LIST <<<"$ARMS"
for arm_code in "${ARM_LIST[@]}"; do
  for rep in $(seq 1 "$REPS"); do
    run_rep_b6 "$arm_code" "$rep" || overall_exit=1
  done
done

echo "run_b6.sh: results in $RESULTS_DIR" >&2
python3 "$HERE/report.py" "$RESULTS_DIR" >"$RESULTS_DIR/summary.md"
echo "run_b6.sh: summary at $RESULTS_DIR/summary.md" >&2

exit $overall_exit
