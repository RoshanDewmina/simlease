# bench/late_acquire.sh — sourced by workload.sh (BENCH_LATE_ACQUIRE_SCRIPT)
# right after the build step. Acquires the lease only now — install/launch
# are the only phases that need a booted device (B5 rework, P1-6 option b).
# Contract with workload.sh: export BENCH_UDID; append one timed "acquire"
# step JSON to $STEPS_FILE. Release is the arm wrapper's job via the lease-id
# file (this shell can be killed at any point; the wrapper's trap survives
# workload failure, and --rm binds the lease to the wrapper as backstop).

_la_start="$(mono_ns)"
_la_json="$("$SIMURGH_BIN" acquire --model "${BENCH_SIM_NAME:-iPhone 17 Pro}" --wait --rm \
  --label "bench=$BENCH_RUN_ID" --label "agent=$BENCH_AGENT" --json)"
_la_exit=$?
_la_end="$(mono_ns)"

_la_log="$(tail -c 1000 <<<"$_la_json")"
jq -nc --argjson s "$_la_start" --argjson e "$_la_end" --argjson d "$((_la_end - _la_start))" \
  --argjson ec "$_la_exit" --arg log "$_la_log" \
  '{step:"acquire", start_ns:$s, end_ns:$e, duration_ns:$d, exit_code:$ec, incidents:[], log:$log}' >>"$STEPS_FILE"

if [[ $_la_exit -eq 0 ]]; then
  BENCH_UDID="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1])["lease"]["device"]["udid"])' "$_la_json")"
  export BENCH_UDID
  _la_lease_id="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1])["lease"]["id"])' "$_la_json")"
  if [[ -n "${BENCH_LEASE_ID_FILE:-}" ]]; then
    echo "$_la_lease_id" >"$BENCH_LEASE_ID_FILE"
  fi
fi
