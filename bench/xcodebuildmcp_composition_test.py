#!/usr/bin/env python3
"""Unit tests for bench/xcodebuildmcp_composition.py."""
from __future__ import annotations

import pathlib
import sys
import unittest

HERE = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import xcodebuildmcp_composition as xbmcp  # noqa: E402


class XcodeBuildMCPCompositionTests(unittest.TestCase):
    def test_composes(self) -> None:
        self.assertEqual(
            xbmcp.composition_verdict(
                build_ok=True, dd_under_lease=True, no_global_pollution=True
            ),
            "composes",
        )

    def test_partial_global_dd(self) -> None:
        self.assertEqual(
            xbmcp.composition_verdict(
                build_ok=True, dd_under_lease=True, no_global_pollution=False
            ),
            "partial_global_dd_growth",
        )

    def test_does_not_compose(self) -> None:
        self.assertEqual(
            xbmcp.composition_verdict(
                build_ok=False, dd_under_lease=False, no_global_pollution=True
            ),
            "does_not_compose",
        )

    def test_dd_populated(self) -> None:
        self.assertTrue(xbmcp.dd_populated("/tmp/lease-dd", 3))
        self.assertFalse(xbmcp.dd_populated("/tmp/lease-dd", 0))
        self.assertFalse(xbmcp.dd_populated("", 5))

    def test_global_pollution_check(self) -> None:
        self.assertTrue(xbmcp.no_new_global_lancer_dd(2, 2))
        self.assertTrue(xbmcp.no_new_global_lancer_dd(2, 1))
        self.assertFalse(xbmcp.no_new_global_lancer_dd(2, 3))


if __name__ == "__main__":
    unittest.main()
