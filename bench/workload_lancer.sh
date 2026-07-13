#!/usr/bin/env bash
# bench/workload_lancer.sh — B6 real-world workload (BENCHMARK_PLAN.md
# B6 section): build the real Lancer app for simulator -> install ->
# launch -> assert healthy 5s -> screenshot -> terminate, against a
# given disposable git worktree.
#
# Lancer has no compiled per-agent badge (unlike the toy SampleApp
# fixture) — modifying a live, actively-developed shared app just for
# benchmarking is out of scope. Evidence-integrity/collision detection
# instead reuses workload.sh's install-hash-comparison technique: hash
# the installed binary right after install, re-hash right before
# terminate (re-resolving the container path fresh each time), and
# flag a mismatch as install_clobber. No test targets are ever run —
# LancerUITests includes relay/pairing flows, and the project's own
# docs warn the daemon has a single relay pairing slot; this workload
# only builds, installs, launches, and screenshots.
#
# Env: BENCH_ARM, BENCH_AGENT, BENCH_UDID, BENCH_WORKTREE (required)
#      BENCH_DESTINATION (default: id=$BENCH_UDID)
#      BENCH_DD, BENCH_SWIFTPM (empty = omit flag)
#      BENCH_BUILD_PREFIX (e.g. "simurgh exec <lease-id> --")
#      BENCH_SCENARIO, BENCH_REP, BENCH_OUT (required)
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib.sh"

: "${BENCH_ARM:?BENCH_ARM required}"
: "${BENCH_AGENT:?BENCH_AGENT required}"
: "${BENCH_UDID:?BENCH_UDID required}"
: "${BENCH_WORKTREE:?BENCH_WORKTREE required}"
: "${BENCH_SCENARIO:?BENCH_SCENARIO required}"
: "${BENCH_REP:?BENCH_REP required}"
: "${BENCH_OUT:?BENCH_OUT required}"

SCHEME="Lancer"
BUNDLE_ID="dev.lancer.mobile"
BENCH_DESTINATION="${BENCH_DESTINATION:-platform=iOS Simulator,id=$BENCH_UDID}"
BENCH_DD="${BENCH_DD:-}"
BENCH_SWIFTPM="${BENCH_SWIFTPM:-}"
BENCH_BUILD_PREFIX="${BENCH_BUILD_PREFIX:-}"

STEPS_FILE="$(mktemp)"
CONSOLE_LOG="$(mktemp)"
cleanup() { rm -f "$STEPS_FILE" "$CONSOLE_LOG" "$CONSOLE_LOG.pid"; }
trap cleanup EXIT

xcb_common=(-project "$BENCH_WORKTREE/Lancer.xcodeproj" -scheme "$SCHEME" -configuration Debug)
if [[ -z "$BENCH_BUILD_PREFIX" ]]; then
  xcb_common+=(-destination "$BENCH_DESTINATION")
fi

# --- build (real Lancer build — minutes, not seconds; this is the
#     point: long real builds widen every race window) ----------------
build_cmd=(xcodebuild build "${xcb_common[@]}")
[[ -n "$BENCH_DD" ]] && build_cmd+=(-derivedDataPath "$BENCH_DD")
[[ -n "$BENCH_SWIFTPM" ]] && build_cmd+=(-clonedSourcePackagesDirPath "$BENCH_SWIFTPM")
if [[ -n "$BENCH_BUILD_PREFIX" ]]; then
  # shellcheck disable=SC2206
  prefix_arr=($BENCH_BUILD_PREFIX)
  build_cmd=("${prefix_arr[@]}" "${build_cmd[@]}")
fi
build_json="$(bench_step build "${build_cmd[@]}")"
build_exit="$(jq -r '.exit_code' <<<"$build_json")"
echo "$build_json" >>"$STEPS_FILE"

# --- resolve built product path ----------------------------------------
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

# --- install --------------------------------------------------------
install_exit=1
install_json='{"step":"install","start_ns":0,"end_ns":0,"duration_ns":0,"exit_code":1,"incidents":[{"type":"launch_destination_failure","evidence":"build failed or app bundle not found, install skipped"}],"log":""}'
if [[ "$build_exit" -eq 0 && -d "$app_path" ]]; then
  install_json="$(bench_step install xcrun simctl install "$BENCH_UDID" "$app_path")"
  install_exit="$(jq -r '.exit_code' <<<"$install_json")"
fi
echo "$install_json" >>"$STEPS_FILE"

hash_after_install=""
container=""
if [[ "$install_exit" -eq 0 ]]; then
  container="$(xcrun simctl get_app_container "$BENCH_UDID" "$BUNDLE_ID" 2>/dev/null)"
  hash_after_install="$(bench_hash_file "$container/Lancer")"
fi

# --- launch: assert healthy 5s via --console-pty stdout capture -------
launch_start="$(mono_ns)"
launch_exit=1
launch_incidents="[]"
if [[ "$install_exit" -eq 0 ]]; then
  xcrun simctl terminate "$BENCH_UDID" "$BUNDLE_ID" >/dev/null 2>&1 || true
  (xcrun simctl launch --console-pty "$BENCH_UDID" "$BUNDLE_ID" >"$CONSOLE_LOG" 2>&1 &
   echo $! >"$CONSOLE_LOG.pid")
  sleep 5
  # No badge to check — "healthy" here means the process is still
  # resident (simctl launch didn't immediately hand back a dead PID)
  # and produced some console output, not a silent crash-on-launch.
  if xcrun simctl spawn "$BENCH_UDID" launchctl list 2>/dev/null | grep -q "$BUNDLE_ID"; then
    launch_exit=0
  else
    launch_incidents='[{"type":"launch_destination_failure","evidence":"app not resident in launchctl list 5s after launch"}]'
  fi
else
  launch_incidents='[{"type":"launch_destination_failure","evidence":"install failed, launch skipped"}]'
fi
launch_end="$(mono_ns)"
launch_json="$(jq -nc --argjson s "$launch_start" --argjson e "$launch_end" \
  --argjson d "$((launch_end - launch_start))" --argjson ec "$launch_exit" \
  --argjson inc "$launch_incidents" --arg log "$(tail -c 4000 "$CONSOLE_LOG")" \
  '{step:"launch", start_ns:$s, end_ns:$e, duration_ns:$d, exit_code:$ec, incidents:$inc, log:$log}')"
echo "$launch_json" >>"$STEPS_FILE"

# --- watch for relay/port-contention signals in the console log
#     (BENCHMARK_PLAN.md B6: "if B6 surfaces it, log as a v0.2
#     requirement with evidence" — this never acts on it, only detects
#     and records). ----------------------------------------------------
port_contention_incidents="[]"
if grep -qiE 'address already in use|bind.*failed|port.*(in use|conflict)|EADDRINUSE' "$CONSOLE_LOG" 2>/dev/null; then
  evidence="$(grep -iE 'address already in use|bind.*failed|port.*(in use|conflict)|EADDRINUSE' "$CONSOLE_LOG" | head -1)"
  port_contention_incidents="$(jq -nc --arg e "$evidence" '[{type:"port_contention", evidence:$e}]')"
fi

# --- screenshot (evidence artifact) -----------------------------------
screenshot_path="${BENCH_OUT%.json}.png"
screenshot_exit=1
screenshot_json='{"step":"screenshot","start_ns":0,"end_ns":0,"duration_ns":0,"exit_code":1,"incidents":[],"log":"skipped: app not healthy"}'
if [[ "$launch_exit" -eq 0 ]]; then
  screenshot_json="$(bench_step screenshot xcrun simctl io "$BENCH_UDID" screenshot "$screenshot_path")"
  screenshot_exit="$(jq -r '.exit_code' <<<"$screenshot_json")"
fi
echo "$screenshot_json" >>"$STEPS_FILE"

# --- install-over-each-other: re-resolve container fresh, re-hash -----
hash_before_terminate=""
clobbered=false
if [[ -n "$container" ]]; then
  container_now="$(xcrun simctl get_app_container "$BENCH_UDID" "$BUNDLE_ID" 2>/dev/null)"
  hash_before_terminate="$(bench_hash_file "$container_now/Lancer")"
  if [[ -n "$hash_after_install" && "$hash_after_install" != "$hash_before_terminate" ]]; then
    clobbered=true
  fi
fi
clobber_json="$(jq -nc --arg h1 "$hash_after_install" --arg h2 "$hash_before_terminate" --argjson clobbered "$clobbered" \
  '{hash_after_install:$h1, hash_before_terminate:$h2, clobbered:$clobbered}')"

# --- terminate (before killing the console-pty wrapper — see
#     workload.sh's commit history for why the order matters) ---------
terminate_json="$(bench_step terminate xcrun simctl terminate "$BENCH_UDID" "$BUNDLE_ID")"
echo "$terminate_json" >>"$STEPS_FILE"
if [[ -f "$CONSOLE_LOG.pid" ]]; then
  kill "$(cat "$CONSOLE_LOG.pid")" >/dev/null 2>&1 || true
fi

all_incidents="$(jq -s '[.[].incidents[]?]' "$STEPS_FILE")"
all_incidents="$(jq -c --argjson extra "$port_contention_incidents" '. + $extra' <<<"$all_incidents")"
if [[ "$clobbered" == "true" ]]; then
  all_incidents="$(jq -c '. + [{type:"install_clobber", evidence:"installed binary hash changed between install and terminate"}]' <<<"$all_incidents")"
fi

success=true
[[ "$build_exit" -ne 0 || "$install_exit" -ne 0 || "$launch_exit" -ne 0 ]] && success=false

steps_array="$(jq -s '.' "$STEPS_FILE")"
total_ns="$(jq '[.[].duration_ns] | add' <<<"$steps_array")"

jq -n \
  --arg scenario "$BENCH_SCENARIO" \
  --arg arm "$BENCH_ARM" \
  --arg agent "$BENCH_AGENT" \
  --arg rep "$BENCH_REP" \
  --arg udid "$BENCH_UDID" \
  --arg worktree "$BENCH_WORKTREE" \
  --arg ts "$(iso_ts)" \
  --argjson steps "$steps_array" \
  --argjson clobber "$clobber_json" \
  --argjson incidents "$all_incidents" \
  --argjson total_ns "$total_ns" \
  --argjson success "$success" \
  '{scenario:$scenario, arm:$arm, agent:$agent, rep:($rep|tonumber), udid:$udid, worktree:$worktree, timestamp:$ts,
    steps:$steps, install_clobber:$clobber, incidents:$incidents,
    total_duration_ns:$total_ns, success:$success}' >"$BENCH_OUT"

cat "$BENCH_OUT"
[[ "$success" == "true" ]] && exit 0 || exit 1
