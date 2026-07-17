#!/usr/bin/env bash
# bench/run.sh — matrix driver (BENCHMARK_PLAN.md §7).
#
# Usage:
#   run.sh --scenario B1 [--reps N] [--arms N,D,S] [--out DIR]
#          [--retries K] [--simurgh-mode fresh|reuse]
#
# --retries K (default 1 = off): retry-to-green (P0-2). A failed
#   agent-task retries under the same arm and rules, up to K attempts
#   total; retry latency is part of the rep's wall-clock. Per-rep
#   time-to-N-green + attempts land in green-log.jsonl.
# --simurgh-mode reuse (default fresh): ARM-S holds ONE lease per agent
#   across all reps (acquired up front with --reuse-if-exists +
#   per-agent labels, released after the last rep) instead of paying a
#   fresh clone+boot every rep — this mirrors how a real agent iterates
#   on a task. R-WP1 ships real --reuse-if-exists matching (Spec + exact
#   Labels including project + stable Owner.Agent/SessionID); the harness
#   still holds one lease per agent across reps for measured economics.
#   Bench acquires pass per-agent labels but do not set SIMURGH_SESSION_ID
#   or project — cross-rep broker reuse needs those; here reuse is primarily
#   from the harness keeping the same lease_id/udid via REUSE_LEASE_DIR.
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
RETRIES=1
SIMURGH_MODE="fresh"

usage() {
  echo "usage: run.sh --scenario B1..B5 [--reps N] [--arms N,D,S] [--out DIR] [--retries K] [--simurgh-mode fresh|reuse]" >&2
  exit 2
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --scenario) SCENARIO="$2"; shift 2 ;;
    --reps) REPS="$2"; shift 2 ;;
    --arms) ARMS="$2"; shift 2 ;;
    --out) OUT_ROOT="$2"; shift 2 ;;
    --retries) RETRIES="$2"; shift 2 ;;
    --simurgh-mode) SIMURGH_MODE="$2"; shift 2 ;;
    -h|--help) usage ;;
    *) echo "run.sh: unknown flag $1" >&2; usage ;;
  esac
done
[[ -z "$SCENARIO" ]] && usage
[[ "$SIMURGH_MODE" != "fresh" && "$SIMURGH_MODE" != "reuse" ]] && usage

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

# Sweep leftover bench-*-named devices from earlier scenario runs before
# starting — ARM-D deliberately doesn't delete its own devices within a
# run (that residue is the measured DIY cost), so without this sweep
# they accumulate silently across separate run.sh invocations. Safe per
# the harness's own naming invariant: only ever deletes bench-*-named
# devices, never simurgh-* or anything else.
# Scratch sweep (P2-9, the 40 GB lesson): DIY's per-agent scratch
# DerivedData under results/.scratch/ accumulates unbounded across
# separate invocations and once genuinely exhausted this Mac's disk
# mid-run. Swept at startup like bench-* devices. Only ever deletes
# under bench/results/ — within the harness's own safety invariant.
if [[ -d "$HERE/results/.scratch" ]]; then
  scratch_size="$(du -sh "$HERE/results/.scratch" 2>/dev/null | awk '{print $1}')"
  echo "run.sh: sweeping bench/results/.scratch (${scratch_size:-0})" >&2
  rm -rf "$HERE/results/.scratch"
fi

for udid in $(xcrun simctl list devices -j 2>/dev/null | python3 -c '
import json, sys
d = json.load(sys.stdin)
for rt, devs in d.get("devices", {}).items():
    for dev in devs:
        if dev.get("name", "").startswith("bench-"):
            print(dev["udid"])
'); do
  xcrun simctl delete "$udid" >/dev/null 2>&1 || true
done

SIMURGH_BIN="${SIMURGH_BIN:-$HERE/.bin/simurgh}"
DAEMON_STARTED_BY_US=0
# B5 (BENCHMARK_PLAN.md §2: "6 agents (capacity 3)") needs a
# deterministic capacity rather than whatever ComputeCapacity derives
# on this Mac. Empty = daemon's normal auto-compute.
CAPACITY_OVERRIDE=""
[[ "$SCENARIO" == "B5" ]] && CAPACITY_OVERRIDE="3"

ensure_daemon() {
  if [[ ! -x "$SIMURGH_BIN" ]]; then
    echo "run.sh: building simurgh CLI to $SIMURGH_BIN" >&2
    mkdir -p "$(dirname "$SIMURGH_BIN")"
    (cd "$HERE/.." && go build -o "$SIMURGH_BIN" ./cmd/simurgh)
  fi
  local running=false
  "$SIMURGH_BIN" status --json >/dev/null 2>&1 && running=true
  if [[ -n "$CAPACITY_OVERRIDE" && "$running" == "true" ]]; then
    # A daemon from an earlier scenario/session may already be up with
    # the wrong (auto-computed) capacity — a capacity override must
    # start fresh to actually take effect, not silently reuse it.
    echo "run.sh: capacity override requested, restarting daemon to apply it" >&2
    pkill -f "simurgh daemon run" 2>/dev/null || true
    sleep 1
    running=false
  fi
  if [[ "$running" != "true" ]]; then
    echo "run.sh: starting simurgh daemon${CAPACITY_OVERRIDE:+ (SIMURGH_CAPACITY=$CAPACITY_OVERRIDE)}" >&2
    SIMURGH_CAPACITY="$CAPACITY_OVERRIDE" nohup "$SIMURGH_BIN" daemon run >"$RESULTS_DIR/daemon.log" 2>&1 &
    echo $! >"$RESULTS_DIR/daemon.pid"
    DAEMON_STARTED_BY_US=1
    for _ in $(seq 1 20); do
      "$SIMURGH_BIN" status --json >/dev/null 2>&1 && break
      sleep 0.5
    done
  fi
}

# --- reuse mode (P0-1): one long-lived lease per agent for the whole
#     ARM-S cell, acquired before rep 1, released after the last rep. ---
REUSE_LEASE_DIR="$RESULTS_DIR/.reuse-leases"

reuse_acquire_all() {
  mkdir -p "$REUSE_LEASE_DIR"
  local agent
  for agent in "${AGENTS[@]}"; do
    local t0 t1 acquire_json
    t0="$(mono_ns)"
    acquire_json="$("$SIMURGH_BIN" acquire --model "${BENCH_SIM_NAME:-iPhone 17 Pro}" --wait --reuse-if-exists \
      --label "bench=$RUN_ID" --label "agent=$agent" --json)"
    local rc=$?
    t1="$(mono_ns)"
    if [[ $rc -ne 0 ]]; then
      echo "run.sh: reuse-mode acquire failed for agent $agent: $acquire_json" >&2
      return 1
    fi
    python3 -c '
import json, sys
d = json.loads(sys.argv[1])
print(json.dumps({"lease_id": d["lease"]["id"], "udid": d["lease"]["device"]["udid"]}))
' "$acquire_json" >"$REUSE_LEASE_DIR/$agent.json"
    jq -nc --argjson s "$t0" --argjson e "$t1" --argjson d "$((t1 - t0))" --arg agent "$agent" \
      '{agent:$agent, start_ns:$s, end_ns:$e, duration_ns:$d, mode:"reuse", note:"one acquire for the whole cell"}' \
      >"$RESULTS_DIR/reuse-acquire-${agent}.json"
  done
  return 0
}

reuse_release_all() {
  local f
  for f in "$REUSE_LEASE_DIR"/*.json; do
    [[ -f "$f" ]] || continue
    "$SIMURGH_BIN" release "$(jq -r '.lease_id' "$f")" --json >/dev/null 2>&1 || true
    rm -f "$f"
  done
}

cleanup_on_exit() {
  reuse_release_all
  if [[ "$DAEMON_STARTED_BY_US" == "1" && -f "$RESULTS_DIR/daemon.pid" ]]; then
    kill "$(cat "$RESULTS_DIR/daemon.pid")" 2>/dev/null || true
  fi
}
trap cleanup_on_exit EXIT

run_rep() {
  local arm_code="$1" rep="$2"
  local arm_name arm_script
  case "$arm_code" in
    N) arm_name="naive"; arm_script="$HERE/arm_naive.sh" ;;
    D) arm_name="diy"; arm_script="$HERE/arm_diy.sh" ;;
    S) arm_name="simurgh"; arm_script="$HERE/arm_simurgh.sh"; ensure_daemon ;;
    L) arm_name="simurgh-late"; arm_script="$HERE/arm_simurgh_late.sh"; ensure_daemon ;;
    *) echo "run.sh: unknown arm code '$arm_code'" >&2; return 2 ;;
  esac

  echo "== $SCENARIO arm=$arm_name rep=$rep agents=${AGENTS[*]} ==" >&2

  # B5's "peak RAM" metric (plan §2): sample vm_stat every 2s for the
  # rep's duration; peak usage = min observed free+inactive.
  local ram_log="" ram_sampler_pid=""
  if [[ "$SCENARIO" == "B5" ]]; then
    ram_log="$RESULTS_DIR/ram-${arm_name}-rep${rep}.log"
    : >"$ram_log"
    ( while true; do vm_stat >>"$ram_log"; echo "---" >>"$ram_log"; sleep 2; done ) &
    ram_sampler_pid=$!
  fi

  local rep_start_ns loadavg
  rep_start_ns="$(mono_ns)"
  loadavg="$(sysctl -n vm.loadavg 2>/dev/null | tr -d '{}' | xargs)"

  local pids=()
  for agent in "${AGENTS[@]}"; do
    local out_base="$RESULTS_DIR/run-${SCENARIO}-${arm_name}-rep${rep}-${agent}"
    local attempts_file="$RESULTS_DIR/.attempts-${arm_name}-rep${rep}-${agent}"
    (
      export BENCH_AGENT="$agent"
      export BENCH_SCENARIO="$SCENARIO"
      export BENCH_REP="$rep"
      export BENCH_RUN_ID="$RUN_ID"
      export SIMURGH_BIN
      if [[ "$arm_name" == "simurgh" && "$SIMURGH_MODE" == "reuse" && -f "$REUSE_LEASE_DIR/$agent.json" ]]; then
        export BENCH_LEASE_ID="$(jq -r '.lease_id' "$REUSE_LEASE_DIR/$agent.json")"
        export BENCH_PRESET_UDID="$(jq -r '.udid' "$REUSE_LEASE_DIR/$agent.json")"
      fi
      attempt=1
      status=1
      while :; do
        if [[ "$attempt" -eq 1 ]]; then
          export BENCH_OUT="${out_base}.json"
        else
          export BENCH_OUT="${out_base}-try${attempt}.json"
        fi
        "$arm_script"
        status=$?
        echo "$attempt" >"$attempts_file"
        [[ "$status" -eq 0 || "$attempt" -ge "$RETRIES" ]] && exit "$status"
        attempt=$((attempt + 1))
      done
    ) &
    pids+=($!)
  done

  local rep_exit=0
  local agent_statuses=()
  for pid in "${pids[@]}"; do
    if wait "$pid"; then
      agent_statuses+=(0)
    else
      agent_statuses+=(1)
      rep_exit=1
    fi
  done

  # Time-to-N-green (P0-2): wall-clock from rep start until every agent
  # subshell finished (green or retries exhausted), retries included.
  local rep_end_ns=$(( $(mono_ns) ))
  local greens=0 total_attempts=0 attempts_json="{}"
  local i=0
  for agent in "${AGENTS[@]}"; do
    local attempts_file="$RESULTS_DIR/.attempts-${arm_name}-rep${rep}-${agent}"
    local a=1
    [[ -f "$attempts_file" ]] && a="$(cat "$attempts_file")"
    total_attempts=$(( total_attempts + a ))
    [[ "${agent_statuses[$i]}" -eq 0 ]] && greens=$((greens + 1))
    attempts_json="$(jq -c --arg agent "$agent" --argjson a "$a" --argjson green "$([[ ${agent_statuses[$i]} -eq 0 ]] && echo true || echo false)" \
      '. + {($agent): {attempts:$a, green:$green}}' <<<"$attempts_json")"
    rm -f "$attempts_file"
    i=$((i + 1))
  done
  jq -nc --arg scenario "$SCENARIO" --arg arm "$arm_name" --argjson rep "$rep" \
    --argjson wallclock_ns "$((rep_end_ns - rep_start_ns))" \
    --argjson n_agents "${#AGENTS[@]}" --argjson greens "$greens" \
    --argjson all_green "$([[ $greens -eq ${#AGENTS[@]} ]] && echo true || echo false)" \
    --argjson total_attempts "$total_attempts" --argjson max_attempts "$RETRIES" \
    --argjson agents "$attempts_json" --arg loadavg_start "$loadavg" \
    --arg simurgh_mode "$([[ "$arm_name" == "simurgh" ]] && echo "$SIMURGH_MODE" || echo "n/a")" \
    '{scenario:$scenario, arm:$arm, rep:$rep, wallclock_ns:$wallclock_ns, n_agents:$n_agents,
      greens:$greens, all_green:$all_green, total_attempts:$total_attempts, max_attempts:$max_attempts,
      agents:$agents, loadavg_start:$loadavg_start, simurgh_mode:$simurgh_mode}' >>"$RESULTS_DIR/green-log.jsonl"

  if [[ -n "$ram_sampler_pid" ]]; then
    kill "$ram_sampler_pid" 2>/dev/null || true
    local peak_free_inactive_gb
    peak_free_inactive_gb="$(python3 -c "
import re
free=[]
inactive=[]
with open('$ram_log') as f:
    for line in f:
        m = re.search(r'Pages free:\s+(\d+)', line)
        if m: free.append(int(m.group(1)))
        m = re.search(r'Pages inactive:\s+(\d+)', line)
        if m: inactive.append(int(m.group(1)))
page = 16384
if free and inactive:
    n = min(len(free), len(inactive))
    vals = [(free[i]+inactive[i])*page/1024**3 for i in range(n)]
    print(round(min(vals), 2))
else:
    print('null')
")"
    jq -nc --arg arm "$arm_name" --argjson rep "$rep" --arg peak_free_inactive_gb "$peak_free_inactive_gb" \
      '{arm:$arm, rep:$rep, min_observed_free_inactive_gb:($peak_free_inactive_gb|tonumber? // null)}' >>"$RESULTS_DIR/b5-ram-log.jsonl"
  fi

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

# --- B4 residue check: what's left 90s after an agent's whole process
#     tree dies mid-build, per arm's own cleanup mechanism (or lack of
#     one). Ground-truthed against xcrun simctl / the leases dir, not
#     just `simurgh list`'s reported state. --------------------------
bench_b4_residue() {
  local arm_name="$1" victim="$2" lease_info_file="$3"
  case "$arm_name" in
    simurgh)
      local lease_id="" udid="" lease_state="unknown" device_present=false lease_dir_present=false
      if [[ -f "$lease_info_file" ]]; then
        lease_id="$(jq -r '.lease_id' "$lease_info_file")"
        udid="$(jq -r '.udid' "$lease_info_file")"
        lease_state="$("$SIMURGH_BIN" list --json 2>/dev/null | jq -r --arg id "$lease_id" '.leases[]? | select(.id==$id) | .state' 2>/dev/null)"
        [[ -z "$lease_state" ]] && lease_state="not_found_in_list"
        xcrun simctl list devices 2>/dev/null | grep -q "$udid" && device_present=true
        [[ -n "$lease_id" && -d "$HOME/.simurgh/leases/$lease_id" ]] && lease_dir_present=true
      fi
      jq -nc --arg lease_id "$lease_id" --arg udid "$udid" --arg state "$lease_state" \
        --argjson device_present "$device_present" --argjson lease_dir_present "$lease_dir_present" \
        '{arm:"simurgh", victim_lease_id:$lease_id, victim_udid:$udid, lease_state:$state, device_still_present:$device_present, lease_dir_still_present:$lease_dir_present}'
      ;;
    diy)
      local device_name="bench-diy-${victim}-${RUN_ID}"
      local device_present=false
      xcrun simctl list devices 2>/dev/null | grep -q "$device_name" && device_present=true
      jq -nc --arg name "$device_name" --argjson device_present "$device_present" \
        '{arm:"diy", victim_device_name:$name, device_still_present:$device_present}'
      ;;
    naive)
      local stray
      # Anchored (P2-10): a bare `pgrep -fl xcodebuild` substring-matches
      # the unrelated xcodebuildmcp MCP server process.
      stray="$(pgrep -fl 'xcodebuild' 2>/dev/null | grep -E '(^|/| )xcodebuild( |$)' | grep -v grep || true)"
      jq -nc --arg stray "$stray" '{arm:"naive", stray_xcodebuild_processes:$stray}'
      ;;
  esac
}

run_rep_b4() {
  local arm_code="$1" rep="$2"
  local arm_name arm_script
  case "$arm_code" in
    N) arm_name="naive"; arm_script="$HERE/arm_naive.sh" ;;
    D) arm_name="diy"; arm_script="$HERE/arm_diy.sh" ;;
    S) arm_name="simurgh"; arm_script="$HERE/arm_simurgh.sh"; ensure_daemon ;;
    *) echo "run.sh: unknown arm code '$arm_code'" >&2; return 2 ;;
  esac

  local victim="${AGENTS[$((${#AGENTS[@]} - 1))]}"
  echo "== B4 arm=$arm_name rep=$rep agents=${AGENTS[*]} (victim=$victim, killed ~8s into its build) ==" >&2

  local victim_pid_file="$RESULTS_DIR/b4-victim-pid-${arm_name}-rep${rep}"
  local victim_lease_info="$RESULTS_DIR/b4-victim-lease-${arm_name}-rep${rep}.json"
  rm -f "$victim_pid_file" "$victim_lease_info"

  local pids=()
  for agent in "${AGENTS[@]}"; do
    local out="$RESULTS_DIR/run-${SCENARIO}-${arm_name}-rep${rep}-${agent}.json"
    (
      export BENCH_AGENT="$agent"
      export BENCH_SCENARIO="$SCENARIO"
      export BENCH_REP="$rep"
      export BENCH_OUT="$out"
      export BENCH_RUN_ID="$RUN_ID"
      export SIMURGH_BIN
      if [[ "$agent" == "$victim" && "$arm_name" == "simurgh" ]]; then
        # SIGKILL bypasses this script's own release trap entirely — the
        # daemon's watcher has to reclaim instead, and that's the actual
        # mechanism under test. Default is the shipped agent default
        # (--rm: lease dies with the agent process tree, P0-4); override
        # via BENCH_B4_VICTIM_FLAGS to test other reclaim paths (the
        # original 2026-07-13 run used "--inactivity-timeout 20s").
        export BENCH_ACQUIRE_EXTRA_FLAGS="${BENCH_B4_VICTIM_FLAGS:---rm}"
        export BENCH_LEASE_INFO_FILE="$victim_lease_info"
      fi
      "$arm_script"
    ) &
    local this_pid=$!
    [[ "$agent" == "$victim" ]] && echo "$this_pid" >"$victim_pid_file"
    pids+=("$this_pid")
  done

  bash "$HERE/kill_midbuild.sh" 8 "$victim_pid_file" &
  local killer_pid=$!

  for pid in "${pids[@]}"; do
    wait "$pid" 2>/dev/null || true
  done
  wait "$killer_pid" 2>/dev/null || true

  echo "B4: waiting 90s before residue check (plan's metric window)..." >&2
  sleep 90

  local residue
  residue="$(bench_b4_residue "$arm_name" "$victim" "$victim_lease_info")"
  jq -nc --arg arm "$arm_name" --argjson rep "$rep" --argjson residue "$residue" \
    '{scenario:"B4", arm:$arm, rep:$rep, residue_after_90s:$residue}' >>"$RESULTS_DIR/b4-residue-log.jsonl"
  echo "B4 residue (arm=$arm_name rep=$rep): $residue" >&2
}

overall_exit=0
IFS=',' read -ra ARM_LIST <<<"$ARMS"
for arm_code in "${ARM_LIST[@]}"; do
  if [[ "$arm_code" == "S" && "$SIMURGH_MODE" == "reuse" && "$SCENARIO" != "B4" ]]; then
    ensure_daemon
    if ! reuse_acquire_all; then
      overall_exit=1
      continue
    fi
  fi
  for rep in $(seq 1 "$REPS"); do
    if [[ "$SCENARIO" == "B4" ]]; then
      run_rep_b4 "$arm_code" "$rep" || overall_exit=1
    else
      run_rep "$arm_code" "$rep" || overall_exit=1
    fi
  done
  [[ "$arm_code" == "S" && "$SIMURGH_MODE" == "reuse" ]] && reuse_release_all
done

echo "run.sh: results in $RESULTS_DIR" >&2
python3 "$HERE/report.py" "$RESULTS_DIR" >"$RESULTS_DIR/summary.md"
echo "run.sh: summary at $RESULTS_DIR/summary.md" >&2

exit $overall_exit
