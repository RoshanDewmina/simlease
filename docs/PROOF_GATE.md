# Proof gate (v0.1, Option A) — advisory only

Simurgh can evaluate whether a lease's **existing** execution evidence satisfies a
repo-scoped verification policy. This is the first "done means evidence" layer.

**Advisory only.** `simurgh proof status` never blocks acquire/release, never
talks to the daemon except to resolve a lease root, and writes nothing itself.
(Caveat: `--lease` uses the shared CLI IPC dial, which auto-starts the daemon if
it isn't running — like every other lease-resolving command. `--lease-root` is
fully offline.) Agents and CI may ignore the result; exit code `3` is an
advisory unsatisfied signal, not enforcement.

For design context see [plans/PROOF_GATE_EXPLORATION.md](plans/PROOF_GATE_EXPLORATION.md)
§3 Option A.

---

## Policy file

Default path (repo root): `.simurgh/verification.yaml`

```yaml
verification:
  policy_version: 1
  quick: [build, unit_tests]
  full: [clean_build, unit_tests, ui_tests]
  required_for_done: quick
```

| Field | Rules (v1) |
|---|---|
| `policy_version` | Must be `1`. Any other value → structured error naming the version. |
| `quick` / `full` | Fixed tier names. Each must be a non-empty list of known steps. |
| `required_for_done` | Must name `quick` or `full`. Default tier for `proof status` when `--tier` is omitted. |

Unknown step names → structured error naming the step. Empty tier lists → error.
Override the path with `--policy <path>`.

### Known steps (v1)

| Step | Meaning |
|---|---|
| `build` | A successful non-test build (or MCP adapter session evidence). |
| `clean_build` | Successful build evidence **plus** a successful `clean` action. |
| `unit_tests` | Successful `xcodebuild test`, or a result bundle on disk. |
| `ui_tests` | Successful `xcodebuild test` whose argv references a UI test target/plan. |

---

## Evidence sources (lease root)

Lease layout under `<leasesRoot>/<lease-id>/` (same as `simurgh env` / isolator):

| Source | Path | Notes |
|---|---|---|
| PATH-shim audit JSONL | `<leaseRoot>/pathshim/audit.log` | One JSON object per line (`argv`, `exitCode`, `binary`, …). See `internal/integrations/pathshim`. |
| Result bundles | `<leaseRoot>/Results/*.xcresult` | Directories allocated by `isolator.AllocateResultBundlePath`. |
| XcodeBuildMCP session meta | `<leaseRoot>/xcodebuildmcp/adapter.pid.json` or `config.yaml` | Written by the adapter; MCP-driven builds may never hit the PATH shim. |

---

## Evidence heuristics (v1)

These are intentional heuristics, not a full Xcode semantics model.

- **`build`**: an `audit.log` entry that is an `xcodebuild` invocation containing
  action token `build`, not `test`, with `exitCode == 0`; **or** adapter session
  meta present under `xcodebuildmcp/`.
- **`clean_build`**: same build evidence as `build`, **plus** a successful
  `clean` action (`clean build` in one argv, or a separate `clean` entry with
  exit 0).
- **`unit_tests`**: an `audit.log` `xcodebuild` entry with action `test` and
  exit 0; **or** ≥1 `Results/*.xcresult` directory.
- **`ui_tests`**: an `audit.log` `xcodebuild` `test` entry with exit 0 whose
  argv has a token containing `UITests` (target/plan name heuristic).

`xcodebuild` is detected via `binary == "xcodebuild"` or an argv element whose
basename is `xcodebuild` (covers `xcrun xcodebuild …`).

---

## CLI

```text
simurgh proof status [--lease <id> | --lease-root <path>] \
  [--policy <path>] [--tier quick|full] [--json]
```

| Flag | Behavior |
|---|---|
| `--lease` | Resolve lease root via daemon `lease.get` + `SIMURGH_DERIVED_DATA` (same as `simurgh shell`). |
| `--lease-root` | Use a path directly (fixtures / offline). |
| `--policy` | Override `.simurgh/verification.yaml`. |
| `--tier` | Evaluate `quick` or `full`. Default = `required_for_done`. |
| `--json` | Stable machine-readable report. |

### Human output

Per-step `satisfied` / `missing` with a one-line reason, plus overall `verdict`.

### JSON shape

```json
{
  "policy_version": 1,
  "tier": "quick",
  "steps": [
    {"name": "build", "satisfied": true, "reason": "..."},
    {"name": "unit_tests", "satisfied": true, "reason": "..."}
  ],
  "satisfied": true
}
```

When no policy file exists:

```json
{"configured": false, "message": "no policy configured", "satisfied": true}
```

### Exit codes

| Code | Meaning |
|---|---|
| `0` | Tier satisfied, **or** no policy file found (`no policy configured`). |
| `3` | Policy present but unsatisfied (advisory). |
| `1` | Real error: bad YAML, unknown step/version/tier, unresolvable lease, bad flags. |

---

## What this is not

- Not a completion gate on `lease_release` / MCP finish.
- Not fingerprint / freshness checking (source commit + dirty hash).
- Not PATH-shim stage 2 enforcement.
- Not a substitute for reading the audit log or xcresult yourself when debugging.
