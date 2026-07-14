#!/usr/bin/env python3
"""Focused regression tests for bench/redact_secrets.py (no real credentials)."""
from __future__ import annotations

import pathlib
import sys
import unittest

HERE = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import redact_secrets  # noqa: E402


class RedactSecretsTests(unittest.TestCase):
    def test_crsr_prefix(self) -> None:
        fake = "crsr_TESTFAKE00000000000000000000000000000000"
        out = redact_secrets.secrets(f"agent --api-key {fake} --endpoint https://example.test")
        self.assertNotIn("crsr_", out)
        self.assertNotIn("TESTFAKE", out)
        self.assertIn(redact_secrets.MARKER_CURSOR, out)

    def test_api_key_flag(self) -> None:
        out = redact_secrets.secrets("tool --api-key not-a-crsr-but-still-secret --verbose")
        self.assertNotIn("not-a-crsr-but-still-secret", out)
        self.assertIn("--api-key", out)

    def test_env_assignments(self) -> None:
        out = redact_secrets.secrets("CURSOR_API_KEY=supersecret TOKEN=abc")
        self.assertNotIn("supersecret", out)
        self.assertNotIn("abc", out)
        self.assertIn("CURSOR_API_KEY=[REDACTED]", out)

    def test_safe_text(self) -> None:
        s = "found 2 stray processes; pids=[123,456]; command_lines_omitted"
        self.assertEqual(redact_secrets.secrets(s), s)


if __name__ == "__main__":
    unittest.main()
