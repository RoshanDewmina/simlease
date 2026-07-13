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
source "$HERE/lib.sh"

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

# Per-step simctl provision timing (P0-1 instrumentation): DIY's
# create/boot/bootstatus cost was previously invisible to the report —
# it happened before workload.sh's step timing starts, which made
# ARM-S's acquire (which pays clone+boot inside the daemon) look like
# the only arm with a provisioning bill. Recorded as a sidecar so the
# fresh-clone-churn hypothesis can be tested against real numbers.
PROVISION_STEPS="$(mktemp)"
DEVICE_NAME="bench-diy-${BENCH_AGENT}-${BENCH_RUN_ID}"
create_start="$(mono_ns)"
BENCH_UDID="$(xcrun simctl create "$DEVICE_NAME" "$SIM_TYPE" "$RUNTIME")"
create_end="$(mono_ns)"
jq -nc --argjson s "$create_start" --argjson e "$create_end" --argjson d "$((create_end - create_start))" \
  '{step:"simctl_create", start_ns:$s, end_ns:$e, duration_ns:$d}' >>"$PROVISION_STEPS"
boot_start="$(mono_ns)"
xcrun simctl boot "$BENCH_UDID" >/dev/null 2>&1
boot_end="$(mono_ns)"
jq -nc --argjson s "$boot_start" --argjson e "$boot_end" --argjson d "$((boot_end - boot_start))" \
  '{step:"simctl_boot", start_ns:$s, end_ns:$e, duration_ns:$d}' >>"$PROVISION_STEPS"
bs_start="$(mono_ns)"
xcrun simctl bootstatus "$BENCH_UDID" -b >/dev/null 2>&1 || true
bs_end="$(mono_ns)"
jq -nc --argjson s "$bs_start" --argjson e "$bs_end" --argjson d "$((bs_end - bs_start))" \
  '{step:"simctl_bootstatus", start_ns:$s, end_ns:$e, duration_ns:$d}' >>"$PROVISION_STEPS"
if [[ -n "${BENCH_OUT:-}" ]]; then
  jq -s '{arm:"diy", provision_steps:.}' "$PROVISION_STEPS" >"${BENCH_OUT%.json}.provision.json"
fi
rm -f "$PROVISION_STEPS"

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
