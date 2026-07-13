#!/usr/bin/env bash
# bench/arm_simurgh_late.sh — ARM-S, late-acquire pattern (B5 rework,
# P1-6 option b): build OUTSIDE the lease with agent-managed isolated
# paths (a booted device isn't needed to compile), acquire --wait right
# before install, release right after the workload. Lease hold time drops
# from build+install+launch to install+launch, which is what actually
# needs the scarce booted-device capacity under overload.
#
# Isolation note (honest): the build here is isolated by per-agent scratch
# -derivedDataPath/-clonedSourcePackagesDirPath that this script manages —
# NOT by `simurgh exec` flag injection, since no lease exists yet at build
# time. The exclusive-device guarantee at install/launch is unchanged.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib.sh"

: "${BENCH_AGENT:?BENCH_AGENT required}"
: "${BENCH_RUN_ID:?BENCH_RUN_ID required}"
SIMURGH_BIN="${SIMURGH_BIN:-simurgh}"
export SIMURGH_BIN

SCRATCH="$HERE/results/.scratch/s-late/${BENCH_AGENT}-${BENCH_RUN_ID}"
mkdir -p "$SCRATCH/DerivedData" "$SCRATCH/SwiftPM"

LEASE_ID_FILE="$(mktemp)"
release() {
  if [[ -s "$LEASE_ID_FILE" ]]; then
    "$SIMURGH_BIN" release "$(cat "$LEASE_ID_FILE")" --json >/dev/null 2>&1 || true
  fi
  rm -f "$LEASE_ID_FILE"
}
trap release EXIT

export BENCH_ARM="simurgh-late"
export BENCH_UDID=""
export BENCH_DESTINATION="generic/platform=iOS Simulator"
export BENCH_DD="$SCRATCH/DerivedData"
export BENCH_SWIFTPM="$SCRATCH/SwiftPM"
export BENCH_BUILD_PREFIX=""
export BENCH_LATE_ACQUIRE_SCRIPT="$HERE/late_acquire.sh"
export BENCH_LEASE_ID_FILE="$LEASE_ID_FILE"

"$HERE/workload.sh"
exit $?
