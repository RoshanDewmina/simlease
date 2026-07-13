#!/usr/bin/env bash
# bench/workload.sh — one agent task (BENCHMARK_PLAN.md §1):
#   build fixture app -> install -> launch -> assert healthy 5s
#   -> screenshot -> verify screenshot/console shows OWN variant -> terminate
#
# Env-driven so arm_naive.sh / arm_diy.sh / arm_simurgh.sh can drive the
# identical workload with only the isolation flags differing:
#   BENCH_ARM         naive|diy|simurgh
#   BENCH_AGENT       A|B|C|D
#   BENCH_UDID        target simulator UDID (required for install/launch)
#   BENCH_DESTINATION xcodebuild -destination value (default: id=$BENCH_UDID)
#   BENCH_DD          -derivedDataPath value, empty = omit (naive default)
#   BENCH_SWIFTPM     -clonedSourcePackagesDirPath value, empty = omit
#   BENCH_BUILD_PREFIX command prefix, e.g. "simurgh exec <lease-id> --"
#   BENCH_SCENARIO, BENCH_REP   labels only, echoed into the output JSON
#   BENCH_OUT         output JSON path (required)
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib.sh"

: "${BENCH_ARM:?BENCH_ARM required (naive|diy|simurgh)}"
: "${BENCH_AGENT:?BENCH_AGENT required (A|B|C|D)}"
# Late-acquire mode (B5 rework, P1-6 option b): the device is leased AFTER
# the build, right before install — builds don't need a booted device, so
# holding lease capacity through them is pure queue pressure. When
# BENCH_LATE_ACQUIRE_SCRIPT is set, BENCH_UDID starts empty and the script
# (sourced after the build step) acquires and exports it.
if [[ -z "${BENCH_LATE_ACQUIRE_SCRIPT:-}" ]]; then
  : "${BENCH_UDID:?BENCH_UDID required}"
fi
: "${BENCH_SCENARIO:?BENCH_SCENARIO required}"
: "${BENCH_REP:?BENCH_REP required}"
: "${BENCH_OUT:?BENCH_OUT required}"

PROJECT="$HERE/../test/fixtures/SampleApp/SampleApp.xcodeproj"
SCHEME="SampleApp"
BUNDLE_ID="dev.simurgh.fixtures.sampleapp"
BENCH_DESTINATION="${BENCH_DESTINATION:-platform=iOS Simulator,id=$BENCH_UDID}"
BENCH_DD="${BENCH_DD:-}"
BENCH_SWIFTPM="${BENCH_SWIFTPM:-}"
BENCH_BUILD_PREFIX="${BENCH_BUILD_PREFIX:-}"

STEPS_FILE="$(mktemp)"
CONSOLE_LOG="$(mktemp)"
cleanup() { rm -f "$STEPS_FILE" "$CONSOLE_LOG" "$CONSOLE_LOG.pid"; }
trap cleanup EXIT

xcb_common=(-project "$PROJECT" -scheme "$SCHEME" -configuration Debug)
# When BENCH_BUILD_PREFIX is set (arm=simurgh), `simurgh exec` injects
# -destination/-derivedDataPath/-clonedSourcePackagesDirPath itself —
# adding our own here would pass xcodebuild duplicate flags.
if [[ -z "$BENCH_BUILD_PREFIX" ]]; then
  xcb_common+=(-destination "$BENCH_DESTINATION")
fi

# --- build --------------------------------------------------------
build_cmd=(xcodebuild build "${xcb_common[@]}" "OTHER_SWIFT_FLAGS=-DAGENT_${BENCH_AGENT}")
[[ -n "$BENCH_DD" ]] && build_cmd+=(-derivedDataPath "$BENCH_DD")
[[ -n "$BENCH_SWIFTPM" ]] && build_cmd+=(-clonedSourcePackagesDirPath "$BENCH_SWIFTPM")
if [[ -n "$BENCH_BUILD_PREFIX" ]]; then
  # shellcheck disable=SC2206
  prefix_arr=($BENCH_BUILD_PREFIX)
  build_cmd=("${prefix_arr[@]}" "${build_cmd[@]}")
fi
# B4 (kill mid-build) does NOT hook in here: killing just this build
# child would leave the surrounding workload.sh/arm_*.sh process alive
# to run its own normal cleanup on the way out, which defeats the
# point — the plan wants to know what's left behind when an agent's
# *whole* process tree dies uncleanly (crash, force-quit, killed
# terminal), not what happens when only its build step does. run.sh
# kills the entire agent subshell (and everything under it, including
# whatever's running here) from outside instead.
build_json="$(bench_step build "${build_cmd[@]}")"
build_exit="$(jq -r '.exit_code' <<<"$build_json")"
echo "$build_json" >>"$STEPS_FILE"

# --- resolve built product path (deterministic via -showBuildSettings,
#     robust for both per-agent and shared-DerivedData arms) -----------
app_path=""
if [[ "$build_exit" -eq 0 ]]; then
  settings_cmd=(xcodebuild -showBuildSettings "${xcb_common[@]}")
  [[ -n "$BENCH_DD" ]] && settings_cmd+=(-derivedDataPath "$BENCH_DD")
  [[ -n "$BENCH_SWIFTPM" ]] && settings_cmd+=(-clonedSourcePackagesDirPath "$BENCH_SWIFTPM")
  if [[ -n "$BENCH_BUILD_PREFIX" ]]; then
    # shellcheck disable=SC2206
    prefix_arr=($BENCH_BUILD_PREFIX)
    settings_cmd=("${prefix_arr[@]}" "${settings_cmd[@]}")
  fi
  settings_out="$("${settings_cmd[@]}" 2>/dev/null)"
  target_build_dir="$(grep -m1 'TARGET_BUILD_DIR =' <<<"$settings_out" | sed 's/.*= //')"
  full_product_name="$(grep -m1 'FULL_PRODUCT_NAME =' <<<"$settings_out" | sed 's/.*= //')"
  if [[ -n "$target_build_dir" && -n "$full_product_name" ]]; then
    app_path="$target_build_dir/$full_product_name"
  fi
fi

if [[ -n "${BENCH_LATE_ACQUIRE_SCRIPT:-}" ]]; then
  # Sourced, not exec'd: the script must export BENCH_UDID into this
  # shell and append its own timed "acquire" step JSON to $STEPS_FILE.
  source "$BENCH_LATE_ACQUIRE_SCRIPT"
  if [[ -z "${BENCH_UDID:-}" ]]; then
    echo "workload.sh: late acquire failed to produce BENCH_UDID" >&2
    jq -n --arg scenario "$BENCH_SCENARIO" --arg arm "$BENCH_ARM" --arg agent "$BENCH_AGENT" \
      --arg rep "$BENCH_REP" --arg ts "$(iso_ts)" --argjson steps "$(jq -s '.' "$STEPS_FILE")" \
      '{scenario:$scenario, arm:$arm, agent:$agent, rep:($rep|tonumber), udid:"", timestamp:$ts,
        steps:$steps, badge:{expected:$agent, actual:"none", match:false},
        incidents:[{type:"launch_destination_failure", evidence:"late acquire failed, no device"}],
        total_duration_ns:(($steps | map(.duration_ns) | add) // 0), success:false}' >"$BENCH_OUT"
    cat "$BENCH_OUT"
    exit 1
  fi
fi

# `simctl` reporting device state "Booted" is weaker than full boot
# readiness (SpringBoard etc. actually up) — arm_naive.sh/arm_diy.sh
# already wait on `simctl bootstatus` for devices they create
# themselves, but arm_simurgh.sh's device comes pre-leased and this
# script never verified it beyond what the daemon's own WaitBooted
# already did (state-poll only). Under real concurrent CPU load that
# gap widens; a real 10-rep run surfaced this as install/launch
# failing with no observable error — belt-and-braces here regardless
# of arm, cheap when the device's already fully booted.
bench_step boot_wait xcrun simctl bootstatus "$BENCH_UDID" -b >>"$STEPS_FILE"

# --- install --------------------------------------------------------
install_exit=1
install_json='{"step":"install","start_ns":0,"end_ns":0,"duration_ns":0,"exit_code":1,"incidents":[{"type":"launch_destination_failure","evidence":"build failed or app bundle not found, install skipped"}],"log":""}'
if [[ "$build_exit" -eq 0 && -d "$app_path" ]]; then
  install_json="$(bench_step install xcrun simctl install "$BENCH_UDID" "$app_path")"
  install_exit="$(jq -r '.exit_code' <<<"$install_json")"
fi
echo "$install_json" >>"$STEPS_FILE"

# --- install-over-each-other detector: hash the installed binary right
#     after install; re-hash right before terminate. A change means
#     another agent's install clobbered this one mid-run. -------------
hash_after_install=""
container=""
if [[ "$install_exit" -eq 0 ]]; then
  container="$(xcrun simctl get_app_container "$BENCH_UDID" "$BUNDLE_ID" 2>/dev/null)"
  hash_after_install="$(bench_hash_file "$container/SampleApp")"
fi

# --- launch: assert healthy via --console-pty stdout capture ----------
# BENCH_HEALTH_WINDOW_S (default 5, the plan's fixed SLA) is how long
# the badge print has to appear. The wait always runs the FULL window
# (identical wall-clock semantics to the original fixed `sleep 5`, so
# medians stay comparable across runs), but the badge's first-seen
# latency is polled at 0.25s granularity and recorded as
# time_to_badge_ns — the margin data needed to root-cause "window too
# tight under load" instead of guessing at it.
HEALTH_WINDOW_S="${BENCH_HEALTH_WINDOW_S:-5}"
launch_start="$(mono_ns)"
launch_exit=1
launch_incidents="[]"
time_to_badge_ns="null"
if [[ "$install_exit" -eq 0 ]]; then
  xcrun simctl terminate "$BENCH_UDID" "$BUNDLE_ID" >/dev/null 2>&1 || true
  (xcrun simctl launch --console-pty "$BENCH_UDID" "$BUNDLE_ID" >"$CONSOLE_LOG" 2>&1 &
   echo $! >"$CONSOLE_LOG.pid")
  deadline_ns=$(( launch_start + HEALTH_WINDOW_S * 1000000000 ))
  while :; do
    now_ns="$(mono_ns)"
    if [[ "$time_to_badge_ns" == "null" ]] && grep -q 'AGENT_BADGE=' "$CONSOLE_LOG"; then
      time_to_badge_ns=$(( now_ns - launch_start ))
      launch_exit=0
    fi
    [[ "$now_ns" -ge "$deadline_ns" ]] && break
    sleep 0.25
  done
  if [[ "$launch_exit" -ne 0 ]]; then
    launch_incidents="[{\"type\":\"launch_destination_failure\",\"evidence\":\"no AGENT_BADGE print observed within ${HEALTH_WINDOW_S}s healthy window\"}]"
  fi
else
  launch_incidents='[{"type":"launch_destination_failure","evidence":"install failed, launch skipped"}]'
fi
launch_end="$(mono_ns)"
launch_json="$(jq -nc --argjson s "$launch_start" --argjson e "$launch_end" \
  --argjson d "$((launch_end - launch_start))" --argjson ec "$launch_exit" \
  --argjson inc "$launch_incidents" --arg log "$(tail -c 4000 "$CONSOLE_LOG")" \
  --argjson ttb "$time_to_badge_ns" --argjson window "$HEALTH_WINDOW_S" \
  '{step:"launch", start_ns:$s, end_ns:$e, duration_ns:$d, exit_code:$ec, incidents:$inc,
    time_to_badge_ns:$ttb, health_window_s:$window, log:$log}')"
echo "$launch_json" >>"$STEPS_FILE"

# --- screenshot (evidence artifact) -----------------------------------
screenshot_path="${BENCH_OUT%.json}.png"
screenshot_exit=1
screenshot_json='{"step":"screenshot","start_ns":0,"end_ns":0,"duration_ns":0,"exit_code":1,"incidents":[],"log":"skipped: app not healthy"}'
if [[ "$launch_exit" -eq 0 ]]; then
  screenshot_json="$(bench_step screenshot xcrun simctl io "$BENCH_UDID" screenshot "$screenshot_path")"
  screenshot_exit="$(jq -r '.exit_code' <<<"$screenshot_json")"
fi
echo "$screenshot_json" >>"$STEPS_FILE"

# --- verify: badge shown matches this agent's own variant -------------
actual_badge="$(grep -oE 'AGENT_BADGE=[A-Z]+' "$CONSOLE_LOG" | tail -1 | cut -d= -f2)"
badge_match=false
[[ "$actual_badge" == "$BENCH_AGENT" ]] && badge_match=true
badge_json="$(jq -nc --arg expected "$BENCH_AGENT" --arg actual "${actual_badge:-none}" --argjson match "$badge_match" \
  '{expected:$expected, actual:$actual, match:$match}')"

# --- install-over-each-other: re-hash before terminate. Re-resolve the
#     container fresh rather than reusing $container — a clobbering
#     install from another agent can reassign a NEW container UUID
#     entirely, in which case the old path just disappears rather than
#     showing a changed hash at the same path. -------------------------
hash_before_terminate=""
clobbered=false
if [[ -n "$container" ]]; then
  container_now="$(xcrun simctl get_app_container "$BENCH_UDID" "$BUNDLE_ID" 2>/dev/null)"
  hash_before_terminate="$(bench_hash_file "$container_now/SampleApp")"
  if [[ -n "$hash_after_install" && "$hash_after_install" != "$hash_before_terminate" ]]; then
    clobbered=true
  fi
fi
clobber_json="$(jq -nc --arg h1 "$hash_after_install" --arg h2 "$hash_before_terminate" --argjson clobbered "$clobbered" \
  '{hash_after_install:$h1, hash_before_terminate:$h2, clobbered:$clobbered}')"

# --- terminate ----------------------------------------------------
# Order matters: killing the --console-pty wrapper process tears down
# its attached app too (losing the pty connection cascades), so
# `simctl terminate` must run FIRST while the app is still actually
# running — reversed, every single rep logged a spurious "found
# nothing to terminate" incident regardless of real behavior.
terminate_json="$(bench_step terminate xcrun simctl terminate "$BENCH_UDID" "$BUNDLE_ID")"
echo "$terminate_json" >>"$STEPS_FILE"
term_pid_file="$CONSOLE_LOG.pid"
if [[ -f "$term_pid_file" ]]; then
  kill "$(cat "$term_pid_file")" >/dev/null 2>&1 || true
fi

# --- aggregate incidents (per-step + badge + clobber) ------------------
all_incidents="$(jq -s '[.[].incidents[]?]' "$STEPS_FILE")"
if [[ "$badge_match" != "true" ]]; then
  all_incidents="$(jq -c --arg exp "$BENCH_AGENT" --arg act "${actual_badge:-none}" \
    '. + [{type:"wrong_variant", evidence:("expected agent-badge-" + $exp + ", observed " + $act)}]' <<<"$all_incidents")"
fi
if [[ "$clobbered" == "true" ]]; then
  all_incidents="$(jq -c '. + [{type:"install_clobber", evidence:"installed binary hash changed between install and terminate"}]' <<<"$all_incidents")"
fi

success=true
[[ "$build_exit" -ne 0 || "$install_exit" -ne 0 || "$launch_exit" -ne 0 || "$badge_match" != "true" ]] && success=false

steps_array="$(jq -s '.' "$STEPS_FILE")"
total_ns="$(jq '[.[].duration_ns] | add' <<<"$steps_array")"

jq -n \
  --arg scenario "$BENCH_SCENARIO" \
  --arg arm "$BENCH_ARM" \
  --arg agent "$BENCH_AGENT" \
  --arg rep "$BENCH_REP" \
  --arg udid "$BENCH_UDID" \
  --arg ts "$(iso_ts)" \
  --argjson steps "$steps_array" \
  --argjson badge "$badge_json" \
  --argjson clobber "$clobber_json" \
  --argjson incidents "$all_incidents" \
  --argjson total_ns "$total_ns" \
  --argjson success "$success" \
  '{scenario:$scenario, arm:$arm, agent:$agent, rep:($rep|tonumber), udid:$udid, timestamp:$ts,
    steps:$steps, badge:$badge, install_clobber:$clobber, incidents:$incidents,
    total_duration_ns:$total_ns, success:$success}' >"$BENCH_OUT"

cat "$BENCH_OUT"
[[ "$success" == "true" ]] && exit 0 || exit 1
