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
    for path in sorted(glob.glob(os.path.join(results_dir, "run-*.json"))):
        with open(path) as f:
            data = json.load(f)
        data["_file"] = os.path.basename(path)
        runs.append(data)
    return runs


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


def render(results_dir, runs, residue_log):
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
    sys.stdout.write(render(results_dir, runs, residue_log))


if __name__ == "__main__":
    main()
