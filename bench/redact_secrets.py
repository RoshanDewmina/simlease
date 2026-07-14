#!/usr/bin/env python3
"""Scrub credential-like values from bench evidence / preflight text.

Mirrors internal/redact.Secrets. Used as a stdin/stdout filter from bash:
  printf '%s' "$msg" | python3 bench/redact_secrets.py
"""
from __future__ import annotations

import re
import sys

MARKER_CURSOR = "[REDACTED:CURSOR_API_KEY]"
MARKER = "[REDACTED]"

_CRSR = re.compile(r"crsr_[A-Za-z0-9_-]+")
_FLAG = re.compile(r"(?i)(--(?:api-key|token|password|secret))\s+(\S+)")
_ENV = re.compile(
    r"(?i)\b([A-Z0-9_]*(?:API[_-]?KEY|TOKEN|PASSWORD|SECRET|CREDENTIAL)[A-Z0-9_]*)="
    r"([^\s\"']+)"
)


def secrets(text: str) -> str:
    out = _CRSR.sub(MARKER_CURSOR, text)

    def flag_sub(m: re.Match[str]) -> str:
        flag, arg = m.group(1), m.group(2)
        if arg.startswith("[REDACTED"):
            return m.group(0)
        return f"{flag} {MARKER}"

    out = _FLAG.sub(flag_sub, out)
    out = _ENV.sub(r"\1=[REDACTED]", out)
    return out


def main() -> None:
    sys.stdout.write(secrets(sys.stdin.read()))


if __name__ == "__main__":
    main()
