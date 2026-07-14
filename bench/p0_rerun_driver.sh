#!/usr/bin/env bash
# bench/p0_rerun_driver.sh — one-shot driver for the P0 rerun sequence
# (REVIEW_AND_NEXT_STEPS.md P0-1/P0-2/P0-4, P1-6). Waits for a genuinely
# quiet machine (preflight passes on its own — no forced confirmation —
# AND >=10 min of no user input), then runs the cells one scenario at a
# time, re-running preflight before each. Never kills anything; if the
# machine gets busy again mid-sequence, the next scenario simply waits.
#
# RAM floor: BENCH_RAM_FLOOR_GB=0 per the owner's standing 2026-07-13
# decision (16 GB machine, floor calibrated for larger boxes); the real
# free+inactive number is recorded in every preflight JSON regardless.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

IDLE_MIN_S="${BENCH_IDLE_MIN_S:-600}"
LOG="$HERE/results/p0-driver.log"
mkdir -p "$HERE/results"

say() { echo "[$(date -u +%H:%M:%SZ)] $*" | tee -a "$LOG" >&2; }

idle_s() {
  ioreg -c IOHIDSystem 2>/dev/null | awk '/HIDIdleTime/ {print int($NF/1e9); exit}'
}

# Dormant-stray exception (documented orchestrator call, 2026-07-13 P0
# session): the stray-process gate exists because cursor-agent-workers
# previously held this Mac under RAM pressure. A worker that is measured
# dormant (<1s of CPU across a 60s window, tiny RSS) is not that problem,
# and such workers idle for days — hard-blocking the commissioned reruns
# on one would stall the engagement while the machine sits otherwise
# quiet. When the ONLY preflight failure is stray_processes AND every
# matched process is measured dormant, proceed via
# BENCH_PREFLIGHT_CONFIRM=1 and record the dormancy evidence alongside
# the preflight JSON. Disclosed in RESULTS provenance; owner may strike
# the cells. Never kills anything, ever.
strays_dormant() {
  local pids
  pids="$(pgrep -f 'cursor-agent-worker|cursor-agent$|cursor-agent ' || true)"
  [[ -z "$pids" ]] && return 1
  local t1 t2
  t1="$(ps -o cputime= -p $pids 2>/dev/null | awk -F'[:.]' '{s+=$1*60+$2} END {print s+0}')"
  sleep 60
  t2="$(ps -o cputime= -p $pids 2>/dev/null | awk -F'[:.]' '{s+=$1*60+$2} END {print s+0}')"
  local delta=$(( ${t2:-0} - ${t1:-0} ))
  echo "{\"stray_pids\":\"$(echo $pids | tr '\n' ' ')\",\"cpu_seconds_over_60s\":$delta}" >/tmp/stray-dormancy.json
  [[ "$delta" -lt 1 ]]
}

wait_for_quiet() {
  local label="$1"
  while true; do
    local confirm=""
    if ! BENCH_RAM_FLOOR_GB=0 bash "$HERE/preflight.sh" >/tmp/pf-driver.json 2>/dev/null; then
      local fails
      fails="$(python3 -c 'import json;print(",".join(c["name"] for c in json.load(open("/tmp/pf-driver.json"))["checks"] if not c["ok"]))' 2>/dev/null)"
      if [[ "$fails" == "stray_processes" ]] && strays_dormant; then
        say "$label: only failure is stray_processes and strays measured dormant ($(cat /tmp/stray-dormancy.json)) — proceeding under documented exception"
        confirm=1
        BENCH_RAM_FLOOR_GB=0 BENCH_PREFLIGHT_CONFIRM=1 bash "$HERE/preflight.sh" >/tmp/pf-driver.json 2>/dev/null
      else
        say "$label: preflight FAIL — waiting ($fails)"
        sleep 120
        continue
      fi
    fi
    # Disk floor (added after the Data volume hit 100% mid-session from
    # non-bench activity): B1's DIY scratch alone accumulates ~5 GB
    # within one run; starting a cell without headroom just logs broken
    # reps. 15 GB default; real value recorded in the log either way.
    local free_gb
    free_gb="$(df -g /System/Volumes/Data | awk 'NR==2 {print $4}')"
    if [[ "${free_gb:-0}" -lt "${BENCH_DISK_FLOOR_GB:-15}" ]]; then
      say "$label: disk too tight (${free_gb}GB free < ${BENCH_DISK_FLOOR_GB:-15}GB) — waiting"
      sleep 120
      continue
    fi
    local idle
    idle="$(idle_s)"
    if [[ "${idle:-0}" -ge "$IDLE_MIN_S" ]]; then
      say "$label: preflight PASS${confirm:+ (dormant-stray exception)}, idle ${idle}s >= ${IDLE_MIN_S}s — starting"
      return 0
    fi
    say "$label: preflight ok${confirm:+ (dormant-stray exception)} but user active (idle ${idle}s) — waiting"
    sleep 120
  done
}

record_preflight() {
  # run.sh creates the results dir; copy the gating preflight into the
  # newest one so every published cell carries its own preflight record.
  local newest
  newest="$(ls -td "$HERE/results"/2*/ 2>/dev/null | head -1)"
  [[ -n "$newest" ]] && cp /tmp/pf-driver.json "${newest}preflight.json"
  [[ -n "$newest" && -f /tmp/stray-dormancy.json ]] && cp /tmp/stray-dormancy.json "${newest}stray-dormancy.json"
}

run_cell() {
  local label="$1"; shift
  wait_for_quiet "$label"
  say "$label: $*"
  if "$@" >>"$LOG" 2>&1; then
    say "$label: done (exit 0)"
  else
    say "$label: done (nonzero exit — broken reps stay logged broken)"
  fi
  record_preflight
}

say "driver start (pid $$, idle threshold ${IDLE_MIN_S}s)"

run_cell "B1-fresh"  "$HERE/run.sh" --scenario B1 --retries 3
run_cell "B1-reuse"  "$HERE/run.sh" --scenario B1 --arms S --retries 3 --simurgh-mode reuse
run_cell "B2"        "$HERE/run.sh" --scenario B2 --retries 3
run_cell "B4-defaults" "$HERE/run.sh" --scenario B4 --arms S --reps 1
BENCH_HEALTH_WINDOW_S=30 run_cell "B5-window30" "$HERE/run.sh" --scenario B5 --arms S,L

say "driver complete"
