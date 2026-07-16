#!/usr/bin/env bash
# bench/arm_naive.sh — ARM-N: what agents do by default (BENCHMARK_PLAN.md
# ground rule 2). Same simulator (-destination name=...), global
# DerivedData, no coordination. All naive agents in a rep deliberately
# target the SAME concrete device — that's the collision mode this arm
# exists to measure.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib.sh"

SIM_NAME="${BENCH_SIM_NAME:-iPhone 17 Pro}"

resolve_shared_udid() {
  xcrun simctl list devices -j 2>/dev/null | python3 -c '
import json, sys
data = json.load(sys.stdin)
name = sys.argv[1]
for runtime, devs in data.get("devices", {}).items():
    for d in devs:
        if d.get("name") == name and d.get("isAvailable", True):
            print(d["udid"])
            sys.exit(0)
sys.exit(1)
' "$SIM_NAME"
}

BENCH_UDID="$(resolve_shared_udid)" || {
  echo "arm_naive: no available simulator named '$SIM_NAME'" >&2
  exit 1
}

device_state() {
  xcrun simctl list devices -j 2>/dev/null | python3 -c '
import json, sys
data = json.load(sys.stdin)
udid = sys.argv[1]
for runtime, devs in data.get("devices", {}).items():
    for d in devs:
        if d.get("udid") == udid:
            print(d.get("state", ""))
' "$1"
}

PROVISION_STEPS="$(mktemp)"
trap 'rm -f "$PROVISION_STEPS"' EXIT
if [[ "$(device_state "$BENCH_UDID")" != "Booted" ]]; then
  boot_start="$(mono_ns)"
  xcrun simctl boot "$BENCH_UDID" >/dev/null 2>&1 || true
  boot_end="$(mono_ns)"
  jq -nc --argjson s "$boot_start" --argjson e "$boot_end" --argjson d "$((boot_end - boot_start))" \
    '{step:"simctl_boot", start_ns:$s, end_ns:$e, duration_ns:$d}' >>"$PROVISION_STEPS"
  bs_start="$(mono_ns)"
  xcrun simctl bootstatus "$BENCH_UDID" -b >/dev/null 2>&1 || true
  bs_end="$(mono_ns)"
  jq -nc --argjson s "$bs_start" --argjson e "$bs_end" --argjson d "$((bs_end - bs_start))" \
    '{step:"simctl_bootstatus", start_ns:$s, end_ns:$e, duration_ns:$d}' >>"$PROVISION_STEPS"
fi
if [[ -n "${BENCH_OUT:-}" && -s "$PROVISION_STEPS" ]]; then
  jq -s '{arm:"naive", provision_steps:.}' "$PROVISION_STEPS" >"${BENCH_OUT%.json}.provision.json"
fi

export BENCH_ARM="naive"
export BENCH_UDID
export BENCH_DESTINATION="platform=iOS Simulator,name=$SIM_NAME"
export BENCH_DD=""     # global default DerivedData — the naive behavior
export BENCH_SWIFTPM=""
export BENCH_BUILD_PREFIX=""

exec "$HERE/workload.sh"
