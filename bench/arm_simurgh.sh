#!/usr/bin/env bash
# bench/arm_simurgh.sh — ARM-S: acquire -> exec -> release (the product
# under test). Isolation flags are injected by `simurgh exec` itself, not
# hand-rolled here — that's the point of the comparison. Assumes
# `simurgh daemon run` is already up (run.sh's job to start once per
# matrix run, not once per rep).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

: "${BENCH_AGENT:?BENCH_AGENT required}"
: "${BENCH_RUN_ID:?BENCH_RUN_ID required}"
SIMURGH_BIN="${SIMURGH_BIN:-simurgh}"
SIM_NAME="${BENCH_SIM_NAME:-iPhone 17 Pro}"

acquire_json="$("$SIMURGH_BIN" acquire --model "$SIM_NAME" --wait \
  --label "bench=$BENCH_RUN_ID" --label "agent=$BENCH_AGENT" --json)"
acquire_exit=$?
if [[ $acquire_exit -ne 0 ]]; then
  echo "arm_simurgh: acquire failed: $acquire_json" >&2
  exit "$acquire_exit"
fi

LEASE_ID="$(echo "$acquire_json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["lease"]["id"])')"
BENCH_UDID="$(echo "$acquire_json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["lease"]["device"]["udid"])')"

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
