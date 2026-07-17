# Simurgh morning publish — 2026-07-17

**Final HEAD:** `dcc6bf737c097cf7fdffe5ff0864ea43bda8e12c`  
**Matrix:** 17 PASS · 1 BLOCKED · 0 FAIL  
**Pushes:** none

## Shipped tonight

- `ae676d7` — R-WP5 stage 1: `simurgh shell`, audit-only
  `xcodebuild`/`xcrun` PATH shims, safe sibling discovery/install, live
  `darwin_sim` audit proof, seven-tool/hold/unhold docs.
- `d403a51` / merge `2c8332e` — issue #2: dead-owner QUEUED leases are
  cancelled before restart Serve; mid-RPC disconnects produce actionable
  `DAEMON_DISCONNECTED` errors across CLI, MCP, and doctor.
- `53204f1` / merge `be6fb2f` and `35fbdf5` / merge `dcc6bf7` — parallel
  tagged-suite cleanup/polling flakes fixed without weakening ownership or
  daemon-survival assertions.

## R-WP5 truth

**Shipped.** Code, tests, and active docs agree: stage 1 logs bare
`xcodebuild`/`xcrun` invocations and injects no flags. `simurgh-shim` must be
built beside `simurgh` (or be on PATH). Stage 2 flag injection remains
deferred.

## Bugs found and fixed

1. Daemon hard death left a dead-owner QUEUED lease visible for about five
   seconds and printed bare EOF. Fixed and live SIGKILL-verified.
2. Provisioner live-test cleanup rejected other tests' clones despite deleting
   only its own UDID. Fixed with a foreign-clone isolation proof.
3. Parent-survival test used one-shot two-second pings and flaked under full
   simulator load. Fixed with hard-bounded polling; same-PID, ≥30s, and SIGHUP
   checks remain.

GitHub issues
[#1](https://github.com/RoshanDewmina/simurgh/issues/1),
[#2](https://github.com/RoshanDewmina/simurgh/issues/2), and
[#3](https://github.com/RoshanDewmina/simurgh/issues/3) now have live evidence
comments and are closed.

## Final verification

PASS on `dcc6bf7`: both binary builds, `go vet ./...`,
`go test ./... -race -count=1`, full
`go test -tags=darwin_sim ./... -race -count=1` (21/21 packages, 2m11s),
focused negative bundle, and `git diff --check`. Zero booted simulators
remained after verification.

## Remaining risks

1. **Do not push yet:** revoked credential material remains in historical local
   commit `a9443f9`; owner must choose acceptance or authorized history rewrite.
2. W12 is BLOCKED: kernel pressure was normal and no safe production pressure
   trigger exists; unit pressure/queue coverage passed.
3. Bench Phase A was not run: guarded preflight blocked on owner
   `cursor-agent` PID 1202. Disk (109 GB), idle (~1987s), pressure, and simulator
   residue gates passed.

## Recommended owner actions

1. Review commits `ae676d7..dcc6bf7` and decide the `a9443f9` history policy
   before any push.
2. Re-run guarded Phase A only on a quiet host; do not override active owner
   process detection casually.
3. Review [GAP_LIST.md](GAP_LIST.md) for per-workflow evidence and the sole
   BLOCKED cell.
