"""Compute and validate conformance plans from profiles and selectors."""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from dataclasses import replace

from tests.conformance.domain import DomainDeclaration
from tests.conformance.errors import ConfigError
from tests.conformance.model import Layer, Plan, TestIdentity
from tests.conformance.profiles import ProfileDefinition


def _selected(test: TestIdentity, selectors: Sequence[str]) -> bool:
    return any(
        f"conformance/{selector}" in test.labels or f"conformance_{selector}" in test.labels for selector in selectors
    )


def compute_plan(
    tests: Sequence[TestIdentity], selectors: Sequence[str], profiles: Sequence[str] = ("generic",)
) -> Plan:
    selected: dict[str, TestIdentity] = {}
    for test in tests:
        if _selected(test, selectors):
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
    selectors: Sequence[str],
    profiles: Sequence[str] = ("generic",),
) -> Plan:
    by_id = {test.id: test for test in tests}
    checks: list[str] = []
    selected: dict[str, TestIdentity] = {}
    for name in names:
        test = by_id.get(name)
        if test is None:
            checks.append(f"{name} is not a test in this image")
            continue
        if not _selected(test, selectors):
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


def enrich_identities(
    tests: Sequence[TestIdentity],
    domains: Mapping[str, DomainDeclaration],
    catalog: Mapping[str, ProfileDefinition],
    selected_profiles: Sequence[str],
) -> tuple[TestIdentity, ...]:
    enriched: list[TestIdentity] = []
    selected = set(selected_profiles)
    for test in tests:
        domain_name = test.domain
        for label in test.labels:
            if label.startswith("sig/"):
                domain_name = label.split("/", 1)[1]
            elif label.startswith("sig_"):
                domain_name = label.split("_", 1)[1]
        matched_profiles = tuple(
            name
            for name, profile in catalog.items()
            if name in selected
            and any(
                f"conformance/{selector}" in test.labels or f"conformance_{selector}" in test.labels
                for selector in profile.selectors
            )
        )
        domain = domains.get(domain_name)
        component = domain is not None and test.id in domain.component_tests
        layer = Layer.COMPONENT if component or not matched_profiles else Layer.PROFILE
        enriched.append(replace(test, domain=domain_name, layer=layer, profiles=matched_profiles))
    return tuple(enriched)
