# Simurgh Roadmap — milestones with executable acceptance gates

**Version:** 1.0 (2026-07-12). Phase order is law. Every milestone's gate is a
set of literal commands whose clean output defines "done" — no gate, no done.
Work packages within a milestone may run in parallel **iff write-sets are
disjoint** (one worktree per package).

Convention below: `GATE:` lines are the acceptance commands. Integration
gates marked `[mac]` require a real Mac with Xcode + at least one iOS runtime
installed; everything else runs anywhere.

---

## M0 — Skeleton (repo, daemon, socket, doctor)

Packages:
- **M0.1 repo scaffold**: Go module `github.com/<owner>/simurgh` (private),
  cmd/simurgh, internal/{broker,store,provisioner,isolator,watcher,reaper,
  safety,ipc,cli,mcp}, Makefile, CI (build+vet+test+race, macOS runner job
  stubbed), CHANGELOG, this docs/ folder imported.
- **M0.2 IPC + daemon skeleton**: socket create (0600), JSON framing,
  `daemon.ping`, versioned envelope, client auto-start-and-retry.
- **M0.3 store**: state.json journal, atomic rewrite, schemaVersion,
  load/reconcile stub.
- **M0.4 doctor**: checks Xcode/simctl presence, runtimes, socket perms,
  daemon liveness; every failure prints a `fix`.

GATE:
```
go build ./... && go vet ./... && go test -race ./...
simurgh daemon run &   # or install
simurgh doctor         # exit 0, all checks green   [mac]
```

## M1 — Lease core (the product)

Packages:
- **M1.1 broker**: acquire/renew/release/get/list/wait; FIFO queue; capacity
  formula + config override; error triple (code/message/fix) throughout.
- **M1.2 provisioner**: clone base device → `simurgh-clone[...]` naming →
  boot → Booted wait; runtime resolution; provision-failure → FAILED + retry
  hint. (Interface-faked in unit tests; real in `[mac]` suite.)
- **M1.3 watcher + reaper**: PID liveness, heartbeat/TTL/inactivity/hard
  timeouts, `--rm` (release on client-PID exit), reclamation, startup
  reconciliation window, orphan scan by name pattern.
- **M1.4 safety package** *(sensitive)*: centralized deletion guards
  (path-under-`~/.simurgh/leases/` + device-name-pattern rules), property
  tests over adversarial fixtures.
- **M1.5 CLI**: acquire/release/renew/status/list + `--json` + last-lease
  pointer + exit-code contract.

GATE (unit): `go test -race ./internal/broker/... ./internal/safety/...` — includes:
- two concurrent same-spec acquires → distinct UDIDs, both ACTIVE
- exclusive same-device acquire → second QUEUED with position
- SIGKILL'd owner → RECLAIMED within one watcher interval
- prune plan never contains a non-simurgh-named device (property test)

GATE `[mac]` (integration, tagged suite):
```
go test -race -tags darwin_sim ./test/integration/...
# covers: full lifecycle on real simctl; daemon SIGKILL mid-provision →
# restart reconciles with zero orphans and zero non-simurgh deletions
```
Manual smoke: two terminal `simurgh acquire --model "iPhone 17 Pro"` calls →
two booted clones visible in Simulator.app, distinct UDIDs.

## M2 — Isolation + warm templates

Packages:
- **M2.1 isolator**: per-lease dirs, env/flag emission, `simurgh env`,
  `simurgh exec` (incl. auto result-bundle allocation), lease.json snapshot.
  Attribution comment for VibeChard flag matrix.
- **M2.2 warm templates**: `warm create|list|remove`, clone-from-warm path
  in provisioner, reaper exclusion, name-as-truth.
- **M2.3 prune + doctor extensions**: prune plan/apply with buckets
  (orphan/held/active), doctor checks for stale leases + disk usage.

GATE `[mac]`:
```
# From a sample iOS app repo (fixture in test/fixtures/SampleApp):
L=$(simurgh acquire --model "iPhone 17 Pro" --json | jq -r .lease.id)
simurgh exec $L -- xcodebuild -scheme SampleApp -destination "platform=iOS Simulator,id=$SIMURGH_UDID" build
# → DerivedData appears ONLY under ~/.simurgh/leases/$L/DerivedData
# → ~/Library/Developer/Xcode/DerivedData untouched (assert in test)
# Two leases building SampleApp concurrently → both succeed (no build.db race)
# warm template cuts acquire→Booted latency ≥50% vs cold (record numbers)
simurgh release $L && simurgh prune   # plan empty
```

## M3 — Agent surface (MCP + onboarding) & dogfood

Packages:
- **M3.1 MCP server**: five tools per ARCHITECTURE §8, Go MCP SDK, tool
  descriptions reviewed as API.
- **M3.2 `simurgh init`**: writes `.simurgh.yaml`, agent rules snippet,
  `.mcp.json` entry; idempotent.
- **M3.3 dogfood**: register in Claude Code; run TWO parallel Claude Code
  sessions against the SampleApp fixture, each told to build+launch+screenshot;
  capture a dogfood log (what worked, what confused the agents, latencies).

GATE:
```
go test ./internal/mcp/...                     # tool schema + handler tests
# Live: claude mcp add simurgh -- simurgh mcp
# Two concurrent Claude Code sessions each: acquire → build → launch →
# screenshot → release, zero collisions, zero manual UDID handling.
# Evidence: both screenshots + `simurgh status` output mid-run, committed
# to docs/dogfood/2026-xx-xx.md
```

## M4 — Hardening + polish (v0 exit)

- Pressure monitoring before grant (RAM/thermal/disk) with queue-reason.
- Queue `wait` long-poll; `status --queue`.
- Log rotation, `daemon status --logs`.
- Error-message audit: every error path has code+message+fix (grep-able test).
- Docs: README quickstart (Limrun-quality bar), agent guide, troubleshooting.
- Tag `v0.0.1` (private).

GATE: full `[mac]` suite green · dogfood rerun clean · README quickstart
followed verbatim on a fresh Mac account succeeds.

---

## Post-v0 (ordered backlog, do not start early)

- **v0.1** PATH shim (opt-in) — reuse isolator emission; VibeChard-style
  libexec placement.
- **v0.2** Warm *pool* (pre-booted), queue fairness/priorities, per-label
  capacity.
- **v0.3** Physical devices via `devicectl` (readiness probes first).
- **v1** Remote hosts: second-Mac transport behind `Connection`
  (design exists; protocol already carries it).
- **v1.x** Proof-gate layer (report's Pillar 1/2) on top of leases; evidence
  manifest schema from report §11.3.
- **Someday:** dashboard UI, Android, teams. Only with validation evidence
  (PRD §7 thresholds).
