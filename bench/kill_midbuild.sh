#!/usr/bin/env bash
# bench/kill_midbuild.sh — B4 helper (BENCHMARK_PLAN.md scenario B4):
# SIGKILL an agent's ENTIRE process tree partway through its build, to
# measure orphaned-sim / leftover-dir / stray-process cleanup behavior
# across arms.
#
# Usage: kill_midbuild.sh DELAY_SECONDS PID_FILE
#
# Kills the whole descendant tree rooted at the PID in PID_FILE, not
# just a single leaf process: killing only the xcodebuild child would
# leave the wrapping workload.sh/arm_*.sh script alive to run its own
# normal cleanup on the way out, which would make every arm look
# artificially clean regardless of what it actually guarantees. This
# simulates an agent whose whole session dies uncleanly (crash,
# force-quit, killed terminal) — the real failure mode B4 exists to
# measure recovery from.
set -uo pipefail

DELAY="${1:?usage: kill_midbuild.sh DELAY_SECONDS PID_FILE}"
PID_FILE="${2:?usage: kill_midbuild.sh DELAY_SECONDS PID_FILE}"

sleep "$DELAY"

attempts=0
while [[ ! -f "$PID_FILE" && $attempts -lt 20 ]]; do
  sleep 0.25
  attempts=$((attempts + 1))
done

if [[ ! -f "$PID_FILE" ]]; then
  echo "kill_midbuild: $PID_FILE never appeared, nothing to kill" >&2
  exit 1
fi

root_pid="$(cat "$PID_FILE")"
if [[ -z "$root_pid" ]]; then
  echo "kill_midbuild: $PID_FILE was empty" >&2
  exit 1
fi

kill_tree_9() {
  local pid="$1"
  local child
  for child in $(pgrep -P "$pid" 2>/dev/null || true); do
    kill_tree_9 "$child"
  done
  kill -9 "$pid" 2>/dev/null || true
}

if kill -0 "$root_pid" 2>/dev/null; then
  kill_tree_9 "$root_pid"
  echo "kill_midbuild: SIGKILLed process tree rooted at pid $root_pid after ${DELAY}s" >&2
  exit 0
fi

echo "kill_midbuild: pid $root_pid not found or already exited before kill" >&2
exit 1
