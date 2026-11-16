"""Compute and validate conformance plans from profiles and selectors."""

from __future__ import annotations

from collections.abc import Sequence
from dataclasses import replace

from tests.conformance.catalog import Catalog
from tests.conformance.errors import ConfigError
from tests.conformance.model import Plan, Selector, TestIdentity
from tests.conformance.output import plan_key


def matches(selector: Selector, test: TestIdentity) -> bool:
    return any(marker in test.labels for marker in selector.markers)


def assign_profiles(
    tests: Sequence[TestIdentity], catalog: Catalog, selected_profiles: Sequence[str]
) -> tuple[TestIdentity, ...]:
    selected = set(selected_profiles)
    enriched: list[TestIdentity] = []
    for test in tests:
        profiles = tuple(
            profile.id
            for profile in catalog.profiles.values()
            if profile.id in selected
            and any(matches(catalog.selectors[selector_id], test) for selector_id in profile.selectors)
        )
        enriched.append(replace(test, profiles=profiles))
    return tuple(enriched)


def compute_plan(
    tests: Sequence[TestIdentity], selectors: Sequence[Selector], profiles: Sequence[str] = ("generic",)
) -> Plan:
    selected: dict[str, TestIdentity] = {}
    for test in tests:
        if any(matches(selector, test) for selector in selectors):
            selected.setdefault(test.id, test)
    return Plan(
        tests=tuple(selected[key] for key in sorted(selected)),
        profiles=tuple(profiles),
        selectors=tuple(selectors),
        complete=True,
    )


def plan_from_names(
    tests: Sequence[TestIdentity],
    names: Sequence[str],
    selectors: Sequence[Selector],
    profiles: Sequence[str] = ("generic",),
) -> Plan:
    by_id = {test.id: test for test in tests}
    # A saved plan carries the opaque ``key`` so a redacted ``id`` can still be
    # replayed; a hand-written plan may use the raw canonical id instead.
    by_id.update({plan_key(test.id): test for test in tests})
    checks: list[str] = []
    selected: dict[str, TestIdentity] = {}
    for name in names:
        test = by_id.get(name)
        if test is None:
            checks.append(f"{name} is not a test in this image")
            continue
        if not any(matches(selector, test) for selector in selectors):
            checks.append(f"{name} is not selected by the run's profiles")
            continue
        selected.setdefault(test.id, test)
    if checks:
        raise ConfigError(checks)
    full = compute_plan(tests, selectors, profiles)
    full_ids = {test.id for test in full.tests}
    return Plan(
        tests=tuple(selected[key] for key in sorted(selected)),
        profiles=tuple(profiles),
        selectors=tuple(selectors),
        complete=set(selected) == full_ids,
    )
