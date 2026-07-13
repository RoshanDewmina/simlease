#!/usr/bin/env bash
# bench/kill_midbuild.sh — B4 helper (BENCHMARK_PLAN.md scenario B4):
# SIGKILL a build process partway through, to measure orphaned-sim /
# leftover-dir / stray-process cleanup behavior across arms.
#
# Usage: kill_midbuild.sh DELAY_SECONDS PID_FILE
#
# The target agent's workload run must write its xcodebuild subprocess's
# PID to PID_FILE before the build step starts (B4's arm driver sets
# BENCH_BUILD_PID_FILE so workload.sh does this — see workload.sh's
# handling of that variable). This script is intentionally standalone
# (not sourced) so it can be dispatched as its own background job
# racing the workload it targets.
set -uo pipefail

DELAY="${1:?usage: kill_midbuild.sh DELAY_SECONDS PID_FILE}"
PID_FILE="${2:?usage: kill_midbuild.sh DELAY_SECONDS PID_FILE}"

sleep "$DELAY"

# Poll briefly for the PID file to appear — the build step may not have
# started writing it the instant this script's sleep ends.
attempts=0
while [[ ! -f "$PID_FILE" && $attempts -lt 20 ]]; do
  sleep 0.25
  attempts=$((attempts + 1))
done

if [[ ! -f "$PID_FILE" ]]; then
  echo "kill_midbuild: $PID_FILE never appeared, nothing to kill" >&2
  exit 1
fi

target_pid="$(cat "$PID_FILE")"
if [[ -z "$target_pid" ]]; then
  echo "kill_midbuild: $PID_FILE was empty" >&2
  exit 1
fi

if kill -0 "$target_pid" 2>/dev/null; then
  kill -9 "$target_pid"
  echo "kill_midbuild: SIGKILLed pid $target_pid after ${DELAY}s" >&2
  exit 0
fi

echo "kill_midbuild: pid $target_pid not found or already exited before kill" >&2
exit 1
