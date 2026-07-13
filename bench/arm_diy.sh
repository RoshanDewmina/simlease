#!/usr/bin/env bash
# bench/arm_diy.sh — ARM-D: what a careful dev scripts by hand
# (BENCHMARK_PLAN.md ground rule 2). Per-agent simctl-created sim +
# manual -derivedDataPath/-clonedSourcePackagesDirPath/module-cache
# flags. Devices are named bench-diy-* — the harness's own safety
# invariant (HANDOFF_BENCH_PROMPT.md) says cleanup may only ever delete
# bench-*-named devices it created, never simurgh-* or anything else.
#
# Deliberately does NOT delete its device at the end of a rep (only
# shuts it down): a hand-rolled script leaving residue behind is the
# real DIY cost the plan expects to measure ("mostly works but leaves
# residue and needed hand-written setup" — §2). Sweeping bench-* devices
# between scenarios is run.sh's job, not this script's.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

: "${BENCH_AGENT:?BENCH_AGENT required}"
: "${BENCH_RUN_ID:?BENCH_RUN_ID required (unique per matrix run, for device naming)}"

SIM_TYPE="${BENCH_SIM_DEVICETYPE:-com.apple.CoreSimulator.SimDeviceType.iPhone-17-Pro}"
RUNTIME="${BENCH_SIM_RUNTIME:-}"
if [[ -z "$RUNTIME" ]]; then
  RUNTIME="$(xcrun simctl list runtimes -j 2>/dev/null | python3 -c '
import json, sys
d = json.load(sys.stdin)
for r in d.get("runtimes", []):
    if r.get("isAvailable") and r.get("name", "").startswith("iOS"):
        print(r["identifier"]); sys.exit(0)
')"
fi

DEVICE_NAME="bench-diy-${BENCH_AGENT}-${BENCH_RUN_ID}"
BENCH_UDID="$(xcrun simctl create "$DEVICE_NAME" "$SIM_TYPE" "$RUNTIME")"
xcrun simctl boot "$BENCH_UDID" >/dev/null 2>&1
xcrun simctl bootstatus "$BENCH_UDID" -b >/dev/null 2>&1 || true

SCRATCH="$HERE/results/.scratch/diy/${BENCH_AGENT}-${BENCH_RUN_ID}"
mkdir -p "$SCRATCH/DerivedData" "$SCRATCH/SwiftPM" "$SCRATCH/ModuleCache"

export BENCH_ARM="diy"
export BENCH_UDID
export BENCH_DESTINATION="platform=iOS Simulator,id=$BENCH_UDID"
export BENCH_DD="$SCRATCH/DerivedData"
export BENCH_SWIFTPM="$SCRATCH/SwiftPM"
export BENCH_BUILD_PREFIX=""
export CLANG_MODULE_CACHE_PATH="$SCRATCH/ModuleCache"

"$HERE/workload.sh"
workload_exit=$?

xcrun simctl shutdown "$BENCH_UDID" >/dev/null 2>&1 || true

exit $workload_exit
