"""Contract tests for profile expansion, selector union and plan validation."""

from __future__ import annotations

import pytest

from tests.conformance.domain import DomainDeclaration
from tests.conformance.errors import ConfigError
from tests.conformance.model import Classification, Layer, TestIdentity
from tests.conformance.profiles import ProfileDefinition, expand_profiles
from tests.conformance.selection import compute_plan, enrich_identities, plan_from_names


def _test(test_id: str, *labels: str, domain: str = "networking") -> TestIdentity:
    return TestIdentity(
        id=test_id,
        name=f"[{domain}][component] {test_id}: test",
        nodeid=f"tests/e2e/test_{test_id}.py::test_{test_id}",
        labels=frozenset(labels),
        domain=domain,
        layer=Layer.COMPONENT,
        profiles=(),
        classification=Classification.BLOCKING,
    )


def _catalog() -> dict[str, ProfileDefinition]:
    return {
        "generic": ProfileDefinition("generic", ("generic",)),
        "vm": ProfileDefinition("vm", ("vmaas",)),
        "netris": ProfileDefinition("netris", ("netris",)),
    }


def test_expand_profiles_adds_generic_and_unions_selectors() -> None:
    names, selectors = expand_profiles(["vm", "netris"], _catalog())

    assert names == ("generic", "vm", "netris")
    assert selectors == ("generic", "vmaas", "netris")


def test_unknown_profile_is_a_config_error() -> None:
    with pytest.raises(ConfigError) as exc:
        expand_profiles(["frobnicate"], _catalog())

    assert "frobnicate" in "\n".join(exc.value.checks)


def test_plan_is_the_union_with_generic_first_and_deduplicated() -> None:
    tests = [
        _test("A", "conformance_generic"),
        _test("B", "conformance_generic", "conformance_vmaas"),
        _test("C", "conformance_netris"),
    ]

    plan = compute_plan(tests, ["generic", "vmaas", "netris"], ("generic", "vm", "netris"))

    assert tuple(t.id for t in plan.tests) == ("A", "B", "C")
    assert plan.profiles == ("generic", "vm", "netris")
    assert plan.selectors == ("generic", "vmaas", "netris")
    assert plan.complete is True


def test_selectors_do_not_imply_one_another() -> None:
    tests = [_test("A", "conformance_generic"), _test("B", "conformance_netris")]

    plan = compute_plan(tests, ["generic", "vmaas"], ("generic", "vm"))

    assert tuple(t.id for t in plan.tests) == ("A",)


def test_plan_from_names_marks_a_subset_incomplete() -> None:
    tests = [_test("A", "conformance_generic"), _test("B", "conformance_vmaas")]

    full = compute_plan(tests, ["generic", "vmaas"], ("generic", "vm"))
    subset = plan_from_names(tests, ["A"], ["generic", "vmaas"], ("generic", "vm"))

    assert tuple(t.id for t in full.tests) == ("A", "B")
    assert tuple(t.id for t in subset.tests) == ("A",)
    assert subset.complete is False


def test_plan_from_names_rejects_an_unknown_test() -> None:
    with pytest.raises(ConfigError) as exc:
        plan_from_names([_test("A", "conformance_generic")], ["MISSING"], ["generic"], ("generic",))

    assert "MISSING" in "\n".join(exc.value.checks)


def test_plan_from_names_rejects_a_test_no_selected_profile_selects() -> None:
    tests = [_test("A", "conformance_generic"), _test("B", "conformance_netris")]

    with pytest.raises(ConfigError) as exc:
        plan_from_names(tests, ["B"], ["generic", "vmaas"], ("generic", "vm"))

    assert "B" in "\n".join(exc.value.checks)


def test_enrich_identities_uses_the_catalog_for_layer_and_profiles() -> None:
    domain = DomainDeclaration(
        name="networking",
        component_tests=("TC-COMP-01",),
        identity_fields=("name",),
        configuration_errors=(),
        support_condition="",
        registration_source="",
        titles={},
        selectors={"generic": ("TC-COMP-01",), "vmaas": ("TC-PROFILE-01",)},
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

    enriched = enrich_identities(raw, {"networking": domain}, _catalog(), ("generic", "vm"))

    assert enriched[0].layer is Layer.COMPONENT
    assert enriched[1].layer is Layer.PROFILE
    assert enriched[1].profiles == ("vm",)


def test_a_test_can_count_toward_multiple_profiles() -> None:
    catalog = {
        "generic": ProfileDefinition("generic", ("generic",)),
        "vm": ProfileDefinition("vm", ("vmaas",)),
        "full": ProfileDefinition("full", ("vmaas", "storage")),
    }
    raw = [_test("TC-1", "sig_networking", "conformance_vmaas")]

    enriched = enrich_identities(raw, {}, catalog, ("generic", "vm", "full"))

    assert enriched[0].profiles == ("vm", "full")
