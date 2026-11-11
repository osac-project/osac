"""Contract tests for selector union and plan validation."""

from __future__ import annotations

import pytest

from tests.conformance.area import AreaDeclaration
from tests.conformance.errors import ConfigError
from tests.conformance.model import Classification, Layer, TestIdentity
from tests.conformance.selection import compute_plan, enrich_identities, plan_from_names


def _test(test_id: str, *labels: str, area: str = "networking") -> TestIdentity:
    return TestIdentity(
        id=test_id,
        name=f"[{area}][component] {test_id}: test",
        nodeid=f"tests/e2e/test_{test_id}.py::test_{test_id}",
        labels=frozenset(labels),
        area=area,
        layer=Layer.COMPONENT,
        profile=None,
        classification=Classification.BLOCKING,
    )


def test_plan_is_the_union_with_generic_first_and_deduplicated() -> None:
    tests = [
        _test("A", "conformance_generic"),
        _test("B", "conformance_generic", "conformance_vmaas"),
        _test("C", "conformance_netris"),
    ]

    plan = compute_plan(tests, ["vmaas", "netris"])

    assert tuple(t.id for t in plan.tests) == ("A", "B", "C")
    assert plan.selectors == ("generic", "vmaas", "netris")
    assert plan.complete is True


def test_selectors_do_not_imply_one_another() -> None:
    tests = [_test("A", "conformance_generic"), _test("B", "conformance_netris")]

    plan = compute_plan(tests, ["vmaas"])

    assert tuple(t.id for t in plan.tests) == ("A",)


def test_plan_from_names_marks_a_subset_incomplete() -> None:
    tests = [_test("A", "conformance_generic"), _test("B", "conformance_vmaas")]

    full = compute_plan(tests, ["vmaas"])
    subset = plan_from_names(tests, ["A"], ["vmaas"])

    assert tuple(t.id for t in full.tests) == ("A", "B")
    assert tuple(t.id for t in subset.tests) == ("A",)
    assert subset.complete is False


def test_plan_from_names_rejects_an_unknown_test() -> None:
    with pytest.raises(ConfigError) as exc:
        plan_from_names([_test("A", "conformance_generic")], ["MISSING"], ["vmaas"])

    assert "MISSING" in "\n".join(exc.value.checks)


def test_plan_from_names_rejects_a_test_no_selector_selects() -> None:
    tests = [_test("A", "conformance_generic"), _test("B", "conformance_netris")]

    with pytest.raises(ConfigError) as exc:
        plan_from_names(tests, ["B"], ["vmaas"])

    assert "B" in "\n".join(exc.value.checks)


def test_enrich_identities_uses_area_declarations_for_layer_and_profile() -> None:
    area = AreaDeclaration(
        name="networking",
        component_tests=("TC-COMP-01",),
        profiles={"vm": "vmaas"},
        identity_fields=("name",),
        configuration_errors=(),
        support_condition="",
        registration_source="",
        titles={},
        selectors={"vmaas": ("TC-PROFILE-01",)},
        checks=(),
        timeout_defaults={},
        component_job_timeout="60m",
        settings_source="",
        evidence=(),
        reference_components=(),
    )
    raw = [
        _test("TC-COMP-01", "sig_networking", "conformance_generic"),
        _test("TC-PROFILE-01", "sig_networking", "conformance_vmaas"),
    ]

    enriched = enrich_identities(raw, {"networking": area})

    assert enriched[0].layer is Layer.COMPONENT
    assert enriched[1].layer is Layer.PROFILE
    assert enriched[1].profile == "vm"
