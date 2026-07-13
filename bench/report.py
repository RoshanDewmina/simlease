#!/usr/bin/env python3
"""bench/report.py — results/<ts>/run-*.json -> summary.md.

Pure function of the JSON files in RESULTS_DIR: same inputs must produce
byte-identical output (BENCHMARK_PLAN.md §7 acceptance bar), so this
script never reads the clock or any other run-relative state — only the
files themselves, iterated in a fixed sort order.
"""
import glob
import json
import os
import statistics
import sys


def load_runs(results_dir):
    runs = []
    skipped = []
    for path in sorted(glob.glob(os.path.join(results_dir, "run-*.json"))):
        base = os.path.basename(path)
        # Sidecar files (e.g. arm_simurgh.sh's *.acquire.json acquire-
        # timing record, arm_diy.sh's *.provision.json) match this glob
        # but aren't agent-task results.
        if ".acquire." in base or ".provision." in base:
            continue
        # Retry attempts (-tryN suffix, P0-2) are counted by the
        # time-to-N-green table via green-log.jsonl; the per-agent-task
        # table below stays first-attempt-only so it remains comparable
        # with pre-retry runs.
        stem = base[: -len(".json")]
        if stem.rsplit("-", 1)[-1].startswith("try"):
            continue
        try:
            with open(path) as f:
                data = json.load(f)
        except (json.JSONDecodeError, OSError) as exc:
            skipped.append((base, str(exc)))
            continue
        if "scenario" not in data or "arm" not in data:
            skipped.append((base, "missing scenario/arm key"))
            continue
        data["_file"] = base
        runs.append(data)
    if skipped:
        for name, reason in skipped:
            print(f"report.py: skipped {name}: {reason}", file=sys.stderr)
    return runs


def load_green_log(results_dir):
    path = os.path.join(results_dir, "green-log.jsonl")
    entries = []
    if os.path.exists(path):
        with open(path) as f:
            for line in f:
                line = line.strip()
                if line:
                    entries.append(json.loads(line))
    return entries


def load_residue_log(results_dir):
    path = os.path.join(results_dir, "residue-log.jsonl")
    entries = []
    if os.path.exists(path):
        with open(path) as f:
            for line in f:
                line = line.strip()
                if line:
                    entries.append(json.loads(line))
    return entries


def group_key(run):
    return (run["scenario"], run["arm"])


def fmt_ns(ns):
    return f"{ns / 1e9:.2f}s"


def summarize_group(runs):
    durations = sorted(r["total_duration_ns"] for r in runs)
    n = len(durations)
    median = statistics.median(durations) if durations else 0
    lo = durations[0] if durations else 0
    hi = durations[-1] if durations else 0
    successes = sum(1 for r in runs if r.get("success"))
    incident_counts = {}
    for r in runs:
        for inc in r.get("incidents", []):
            t = inc.get("type", "unknown")
            incident_counts[t] = incident_counts.get(t, 0) + 1
    return {
        "n": n,
        "median_ns": median,
        "min_ns": lo,
        "max_ns": hi,
        "successes": successes,
        "incident_counts": incident_counts,
    }


def badge_latency_stats(runs):
    """Per (scenario, arm): time-to-badge latencies from first-attempt
    launch steps that recorded one (older runs predate the field)."""
    groups = {}
    for r in runs:
        for step in r.get("steps", []):
            if step.get("step") == "launch" and step.get("time_to_badge_ns") is not None:
                groups.setdefault(group_key(r), []).append(step["time_to_badge_ns"])
    return groups


def render_green_table(lines, green_log):
    lines.append("## Time-to-N-green (retry-to-green, max attempts per agent-task shown)")
    lines.append("")
    lines.append(
        "Wall-clock per rep until ALL N agents hold a verified-correct result "
        "(retry latency included). `first-try greens` counts agents green on attempt 1."
    )
    lines.append("")
    lines.append(
        "| Scenario | Arm | Mode | Reps | All-green reps | Median to-green | Min | Max "
        "| First-try greens | Attempts used / budget |"
    )
    lines.append("|---|---|---|---|---|---|---|---|---|---|")
    groups = {}
    for e in green_log:
        groups.setdefault((e["scenario"], e["arm"], e.get("simurgh_mode", "n/a")), []).append(e)
    for key in sorted(groups.keys()):
        scenario, arm, mode = key
        entries = groups[key]
        wallclocks = sorted(e["wallclock_ns"] for e in entries)
        all_green = sum(1 for e in entries if e.get("all_green"))
        first_try = sum(
            1
            for e in entries
            for a in e.get("agents", {}).values()
            if a.get("green") and a.get("attempts") == 1
        )
        agent_tasks = sum(e.get("n_agents", 0) for e in entries)
        attempts = sum(e.get("total_attempts", 0) for e in entries)
        budget = sum(e.get("n_agents", 0) * e.get("max_attempts", 1) for e in entries)
        lines.append(
            f"| {scenario} | {arm} | {mode} | {len(entries)} | {all_green}/{len(entries)} "
            f"| {fmt_ns(statistics.median(wallclocks))} | {fmt_ns(wallclocks[0])} | {fmt_ns(wallclocks[-1])} "
            f"| {first_try}/{agent_tasks} | {attempts}/{budget} |"
        )
    lines.append("")


def render(results_dir, runs, residue_log, green_log):
    lines = []
    lines.append(f"# Bench summary — {os.path.basename(results_dir.rstrip('/'))}")
    lines.append("")
    lines.append(f"Raw run files: {len(runs)}")
    lines.append("")

    groups = {}
    for r in runs:
        groups.setdefault(group_key(r), []).append(r)

    lines.append("| Scenario | Arm | n | Success | Median | Min | Max | Incidents |")
    lines.append("|---|---|---|---|---|---|---|---|")
    for key in sorted(groups.keys()):
        scenario, arm = key
        s = summarize_group(groups[key])
        incident_str = (
            ", ".join(f"{k}={v}" for k, v in sorted(s["incident_counts"].items()))
            if s["incident_counts"]
            else "none"
        )
        lines.append(
            f"| {scenario} | {arm} | {s['n']} | {s['successes']}/{s['n']} "
            f"| {fmt_ns(s['median_ns'])} | {fmt_ns(s['min_ns'])} | {fmt_ns(s['max_ns'])} "
            f"| {incident_str} |"
        )
    lines.append("")

    if green_log:
        render_green_table(lines, green_log)

    latency_groups = badge_latency_stats(runs)
    if latency_groups:
        lines.append("## Badge latency (launch → first AGENT_BADGE print, first attempts)")
        lines.append("")
        lines.append("| Scenario | Arm | n with badge | Median | Min | Max |")
        lines.append("|---|---|---|---|---|---|")
        for key in sorted(latency_groups.keys()):
            scenario, arm = key
            vals = sorted(latency_groups[key])
            lines.append(
                f"| {scenario} | {arm} | {len(vals)} | {fmt_ns(statistics.median(vals))} "
                f"| {fmt_ns(vals[0])} | {fmt_ns(vals[-1])} |"
            )
        lines.append("")

    if residue_log:
        lines.append("## Post-rep residue log")
        lines.append("")
        lines.append("| Arm | Rep | simurgh-clone residue count |")
        lines.append("|---|---|---|")
        for entry in residue_log:
            count = entry.get("post_rep_residue", {}).get("count", 0)
            lines.append(f"| {entry.get('arm')} | {entry.get('rep')} | {count} |")
        lines.append("")

    lines.append("## Evidence-integrity findings (wrong-variant badge)")
    lines.append("")
    wrong_variant_runs = [
        r for r in runs if any(i.get("type") == "wrong_variant" for i in r.get("incidents", []))
    ]
    if wrong_variant_runs:
        lines.append("| File | Scenario | Arm | Agent | Rep | Expected | Actual |")
        lines.append("|---|---|---|---|---|---|---|")
        for r in wrong_variant_runs:
            b = r.get("badge", {})
            lines.append(
                f"| {r['_file']} | {r['scenario']} | {r['arm']} | {r['agent']} | {r['rep']} "
                f"| {b.get('expected')} | {b.get('actual')} |"
            )
    else:
        lines.append("None in this results directory.")
    lines.append("")

    return "\n".join(lines) + "\n"


def main():
    if len(sys.argv) != 2:
        print("usage: report.py RESULTS_DIR", file=sys.stderr)
        sys.exit(2)
    results_dir = sys.argv[1]
    runs = load_runs(results_dir)
    residue_log = load_residue_log(results_dir)
    green_log = load_green_log(results_dir)
    sys.stdout.write(render(results_dir, runs, residue_log, green_log))


if __name__ == "__main__":
    main()
