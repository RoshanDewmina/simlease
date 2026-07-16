#!/usr/bin/env python3
"""Unit tests for bench/report.py (P0-2 time-to-green + P0-1 sidecars)."""
from __future__ import annotations

import json
import pathlib
import sys
import tempfile
import unittest

HERE = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import report  # noqa: E402


class ReportTests(unittest.TestCase):
    def test_load_runs_skips_sidecars_and_retries(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            (root / "run-B1-simurgh-rep1-A.json").write_text(
                json.dumps(
                    {
                        "scenario": "B1",
                        "arm": "simurgh",
                        "agent": "A",
                        "rep": 1,
                        "success": True,
                        "total_duration_ns": 1_000_000_000,
                        "incidents": [],
                    }
                )
            )
            (root / "run-B1-simurgh-rep1-A.acquire.json").write_text("{}")
            (root / "run-B1-simurgh-rep1-A-try2.json").write_text(
                json.dumps({"scenario": "B1", "arm": "simurgh", "agent": "A", "rep": 1})
            )
            runs = report.load_runs(str(root))
            self.assertEqual(len(runs), 1)
            self.assertEqual(runs[0]["agent"], "A")

    def test_green_table_from_log(self) -> None:
        green_log = [
            {
                "scenario": "B1",
                "arm": "simurgh",
                "simurgh_mode": "reuse",
                "rep": 1,
                "wallclock_ns": 20_000_000_000,
                "n_agents": 2,
                "all_green": True,
                "total_attempts": 2,
                "max_attempts": 3,
                "agents": {
                    "A": {"attempts": 1, "green": True},
                    "B": {"attempts": 1, "green": True},
                },
            },
            {
                "scenario": "B1",
                "arm": "simurgh",
                "simurgh_mode": "reuse",
                "rep": 2,
                "wallclock_ns": 40_000_000_000,
                "n_agents": 2,
                "all_green": False,
                "total_attempts": 4,
                "max_attempts": 3,
                "agents": {
                    "A": {"attempts": 2, "green": True},
                    "B": {"attempts": 2, "green": False},
                },
            },
        ]
        lines: list[str] = []
        report.render_green_table(lines, green_log)
        text = "\n".join(lines)
        self.assertIn("Time-to-N-green", text)
        self.assertIn("| B1 | simurgh | reuse | 2 | 1/2 |", text)
        self.assertIn("6/12", text)  # total attempts / budget across both reps

    def test_provision_timing_stats(self) -> None:
        sidecars = [
            {
                "arm": "diy",
                "provision_steps": [
                    {"step": "simctl_create", "duration_ns": 1_000_000_000},
                    {"step": "simctl_boot", "duration_ns": 2_000_000_000},
                ],
            },
            {
                "_kind": "acquire",
                "duration_ns": 3_000_000_000,
            },
            {
                "_kind": "reuse_acquire",
                "duration_ns": 500_000_000,
            },
        ]
        stats = report.provision_timing_stats(sidecars)
        self.assertEqual(len(stats[("diy", "simctl_create")]), 1)
        self.assertEqual(len(stats[("simurgh", "acquire")]), 1)
        self.assertEqual(len(stats[("simurgh", "reuse_acquire")]), 1)

    def test_render_on_committed_evidence(self) -> None:
        evidence = HERE / "evidence" / "B1-reuse-20260714T131116Z"
        if not evidence.is_dir():
            self.skipTest("committed B1-reuse evidence missing")
        runs = report.load_runs(str(evidence))
        green_log = report.load_green_log(str(evidence))
        sidecars = report.load_provision_sidecars(str(evidence))
        out = report.render(str(evidence), runs, [], green_log, sidecars)
        self.assertIn("Time-to-N-green", out)
        self.assertIn("reuse_acquire", out)


if __name__ == "__main__":
    unittest.main()
