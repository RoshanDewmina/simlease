#!/usr/bin/env python3
"""Offline verdict logic for bench/check_xcodebuildmcp.sh (B6 P0-3).

Pure function of build exit code + where DerivedData landed — unit-testable
without Lancer, simulators, or npx.
"""


def composition_verdict(
    *,
    build_ok: bool,
    dd_under_lease: bool,
    no_global_pollution: bool,
) -> str:
    if build_ok and dd_under_lease and no_global_pollution:
        return "composes"
    if build_ok and dd_under_lease:
        return "partial_global_dd_growth"
    return "does_not_compose"


def dd_populated(lease_dd: str, entry_count: int) -> bool:
    return bool(lease_dd) and entry_count > 0


def no_new_global_lancer_dd(before_count: int, after_count: int) -> bool:
    return after_count <= before_count
