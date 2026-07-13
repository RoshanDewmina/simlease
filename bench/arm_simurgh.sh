#!/usr/bin/env bash
# bench/arm_simurgh.sh — ARM-S: acquire -> exec -> release (the product
# under test). Isolation flags are injected by `simurgh exec` itself, not
# hand-rolled here — that's the point of the comparison. Assumes
# `simurgh daemon run` is already up (run.sh's job to start once per
# matrix run, not once per rep).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib.sh"

: "${BENCH_AGENT:?BENCH_AGENT required}"
: "${BENCH_RUN_ID:?BENCH_RUN_ID required}"
SIMURGH_BIN="${SIMURGH_BIN:-simurgh}"
SIM_NAME="${BENCH_SIM_NAME:-iPhone 17 Pro}"

# BENCH_ACQUIRE_EXTRA_FLAGS: extra acquire flags, e.g. B4's victim agent
# passes "--inactivity-timeout 20s" so a SIGKILLed process (which cannot
# be caught by this script's own release trap) still gets reclaimed by
# the daemon's watcher within a short, test-relevant window.
# shellcheck disable=SC2206
extra_flags=(${BENCH_ACQUIRE_EXTRA_FLAGS:-})

# Reuse mode (P0-1): when run.sh has already acquired a long-lived lease
# for this agent (one lease held across all reps — how a real agent
# iterates), it passes the lease in via env and this script must not
# acquire or release anything itself: the whole point of the mode is
# that the per-rep fresh clone+boot disappears.
if [[ -n "${BENCH_LEASE_ID:-}" && -n "${BENCH_PRESET_UDID:-}" ]]; then
  export BENCH_ARM="simurgh"
  export BENCH_UDID="$BENCH_PRESET_UDID"
  export BENCH_DD=""
  export BENCH_SWIFTPM=""
  export BENCH_BUILD_PREFIX="$SIMURGH_BIN exec $BENCH_LEASE_ID --"
  exec "$HERE/workload.sh"
fi

# B5's core metric (queued completion vs thrash under capacity
# pressure) happens entirely inside this --wait call, before
# workload.sh's own step timing ever starts — so it has to be timed
# here, not inferred later.
acquire_start="$(mono_ns)"
acquire_json="$("$SIMURGH_BIN" acquire --model "$SIM_NAME" --wait \
  --label "bench=$BENCH_RUN_ID" --label "agent=$BENCH_AGENT" --json "${extra_flags[@]}")"
acquire_exit=$?
acquire_end="$(mono_ns)"
if [[ -n "${BENCH_OUT:-}" ]]; then
  jq -nc --argjson s "$acquire_start" --argjson e "$acquire_end" --argjson d "$((acquire_end - acquire_start))" --argjson exit_code "$acquire_exit" \
    '{start_ns:$s, end_ns:$e, duration_ns:$d, exit_code:$exit_code}' >"${BENCH_OUT%.json}.acquire.json"
fi
if [[ $acquire_exit -ne 0 ]]; then
  echo "arm_simurgh: acquire failed: $acquire_json" >&2
  exit "$acquire_exit"
fi

LEASE_ID="$(echo "$acquire_json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["lease"]["id"])')"
BENCH_UDID="$(echo "$acquire_json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["lease"]["device"]["udid"])')"

# B4/B5 residue checks need to know which lease/device belonged to which
# agent even if this whole process is later SIGKILLed before it would
# otherwise report anything.
if [[ -n "${BENCH_LEASE_INFO_FILE:-}" ]]; then
  jq -nc --arg lease_id "$LEASE_ID" --arg udid "$BENCH_UDID" '{lease_id:$lease_id, udid:$udid}' >"$BENCH_LEASE_INFO_FILE"
fi

release() {
  "$SIMURGH_BIN" release "$LEASE_ID" --json >/dev/null 2>&1 || true
}
trap release EXIT

export BENCH_ARM="simurgh"
export BENCH_UDID
export BENCH_DD=""
export BENCH_SWIFTPM=""
export BENCH_BUILD_PREFIX="$SIMURGH_BIN exec $LEASE_ID --"

"$HERE/workload.sh"
exit $?
