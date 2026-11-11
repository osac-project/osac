"""Compute and validate conformance plans from declared selectors."""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from dataclasses import replace

from tests.conformance.area import AreaDeclaration
from tests.conformance.errors import ConfigError
from tests.conformance.model import Layer, Plan, TestIdentity


def _selectors(order: Sequence[str]) -> tuple[str, ...]:
    result: list[str] = ["generic"]
    for selector in order:
        if selector not in result:
            result.append(selector)
    return tuple(result)


def _selected(test: TestIdentity, selectors: Sequence[str]) -> bool:
    for selector in selectors:
        if f"conformance/{selector}" in test.labels or f"conformance_{selector}" in test.labels:
            return True
    return False


def compute_plan(tests: Sequence[TestIdentity], selectors: Sequence[str]) -> Plan:
    ordered = _selectors(selectors)
    selected: dict[str, TestIdentity] = {}
    for test in tests:
        if _selected(test, ordered):
            selected.setdefault(test.id, test)
    return Plan(tests=tuple(selected[key] for key in sorted(selected)), selectors=ordered, complete=True)


def plan_from_names(tests: Sequence[TestIdentity], names: Sequence[str], selectors: Sequence[str]) -> Plan:
    ordered = _selectors(selectors)
    by_id = {test.id: test for test in tests}
    checks: list[str] = []
    selected: dict[str, TestIdentity] = {}
    for name in names:
        test = by_id.get(name)
        if test is None:
            checks.append(f"{name} is not a test in this image")
            continue
        if not _selected(test, ordered):
            checks.append(f"{name} is not selected by the lab's selectors")
            continue
        selected.setdefault(test.id, test)
    if checks:
        raise ConfigError(checks)
    full = compute_plan(tests, ordered)
    full_ids = {test.id for test in full.tests}
    return Plan(
        tests=tuple(selected[key] for key in sorted(selected)),
        selectors=ordered,
        complete=set(selected) == full_ids,
    )


def enrich_identities(tests: Sequence[TestIdentity], areas: Mapping[str, AreaDeclaration]) -> tuple[TestIdentity, ...]:
    enriched: list[TestIdentity] = []
    for test in tests:
        area_name = test.area
        for label in test.labels:
            if label.startswith("sig/"):
                area_name = label.split("/", 1)[1]
            elif label.startswith("sig_"):
                area_name = label.split("_", 1)[1]
        area = areas.get(area_name)
        if area is None:
            enriched.append(test)
            continue
        if test.id in area.component_tests:
            enriched.append(replace(test, area=area_name, layer=Layer.COMPONENT, profile=None))
            continue
        profile = next(
            (name for name, selector in area.profiles.items() if f"conformance/{selector}" in test.labels or f"conformance_{selector}" in test.labels),
            None,
        )
        if profile is not None:
            enriched.append(replace(test, area=area_name, layer=Layer.PROFILE, profile=profile))
        else:
            enriched.append(replace(test, area=area_name))
    return tuple(enriched)
