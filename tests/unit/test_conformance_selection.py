"""Contract tests for profile expansion, selector union and plan validation."""

from __future__ import annotations

from pathlib import Path

import pytest

from tests.conformance.catalog import Catalog, ProfileDefinition, expand_profiles, load_catalog
from tests.conformance.errors import ConfigError
from tests.conformance.model import Classification, Selector, TestIdentity
from tests.conformance.output import plan_key
from tests.conformance.selection import assign_profiles, compute_plan, plan_from_names


def _test(test_id: str, *labels: str) -> TestIdentity:
    return TestIdentity(
        id=test_id,
        name=f"{test_id}: test",
        nodeid=f"tests/e2e/test_{test_id}.py::test_{test_id}",
        labels=frozenset(labels),
        profiles=(),
        classification=Classification.BLOCKING,
    )


def _catalog() -> Catalog:
    selectors = {
        "generic": Selector("generic", "marker", ("conformance_generic",)),
        "vmaas": Selector("vmaas", "marker", ("conformance_vmaas",)),
        "netris": Selector("netris", "marker", ("conformance_netris",)),
    }
    profiles = {
        "generic": ProfileDefinition("generic", ("generic",)),
        "vm": ProfileDefinition("vm", ("vmaas",)),
        "netris": ProfileDefinition("netris", ("netris",)),
    }
    return Catalog(selectors=selectors, profiles=profiles)


def _selector_ids(catalog: Catalog, selector_ids: tuple[str, ...]) -> tuple[Selector, ...]:
    return tuple(catalog.selectors[selector_id] for selector_id in selector_ids)


def test_expand_profiles_adds_generic_and_unions_selectors() -> None:
    catalog = _catalog()
    names, selectors = expand_profiles(["vm", "netris"], catalog)

    assert names == ("generic", "vm", "netris")
    assert tuple(selector.id for selector in selectors) == ("generic", "vmaas", "netris")


def test_unknown_profile_is_a_config_error() -> None:
    with pytest.raises(ConfigError) as exc:
        expand_profiles(["frobnicate"], _catalog())

    assert "frobnicate" in "\n".join(exc.value.checks)


def test_plan_is_the_union_with_generic_first_and_deduplicated() -> None:
    catalog = _catalog()
    tests = [
        _test("A", "conformance_generic"),
        _test("B", "conformance_generic", "conformance_vmaas"),
        _test("C", "conformance_netris"),
    ]

    plan = compute_plan(tests, _selector_ids(catalog, ("generic", "vmaas", "netris")), ("generic", "vm", "netris"))

    assert tuple(t.id for t in plan.tests) == ("A", "B", "C")
    assert plan.profiles == ("generic", "vm", "netris")
    assert tuple(selector.id for selector in plan.selectors) == ("generic", "vmaas", "netris")
    assert plan.complete is True


def test_selectors_do_not_imply_one_another() -> None:
    catalog = _catalog()
    tests = [_test("A", "conformance_generic"), _test("B", "conformance_netris")]

    plan = compute_plan(tests, _selector_ids(catalog, ("generic", "vmaas")), ("generic", "vm"))

    assert tuple(t.id for t in plan.tests) == ("A",)


def test_plan_from_names_marks_a_subset_incomplete() -> None:
    catalog = _catalog()
    tests = [_test("A", "conformance_generic"), _test("B", "conformance_vmaas")]
    selectors = _selector_ids(catalog, ("generic", "vmaas"))

    full = compute_plan(tests, selectors, ("generic", "vm"))
    subset = plan_from_names(tests, ["A"], selectors, ("generic", "vm"))

    assert tuple(t.id for t in full.tests) == ("A", "B")
    assert tuple(t.id for t in subset.tests) == ("A",)
    assert subset.complete is False


def test_plan_from_names_accepts_the_opaque_plan_key() -> None:
    catalog = _catalog()
    tests = [_test("A", "conformance_generic"), _test("B", "conformance_vmaas")]
    selectors = _selector_ids(catalog, ("generic", "vmaas"))

    subset = plan_from_names(tests, [plan_key("A")], selectors, ("generic", "vm"))

    assert tuple(t.id for t in subset.tests) == ("A",)
    assert subset.complete is False


def test_plan_from_names_rejects_an_unknown_test() -> None:
    catalog = _catalog()
    with pytest.raises(ConfigError) as exc:
        plan_from_names(
            [_test("A", "conformance_generic")], ["MISSING"], _selector_ids(catalog, ("generic",)), ("generic",)
        )

    assert "MISSING" in "\n".join(exc.value.checks)


def test_plan_from_names_rejects_a_test_no_selected_profile_selects() -> None:
    catalog = _catalog()
    tests = [_test("A", "conformance_generic"), _test("B", "conformance_netris")]

    with pytest.raises(ConfigError) as exc:
        plan_from_names(tests, ["B"], _selector_ids(catalog, ("generic", "vmaas")), ("generic", "vm"))

    assert "B" in "\n".join(exc.value.checks)


def test_a_test_can_count_toward_multiple_profiles() -> None:
    catalog = Catalog(
        selectors={
            "generic": Selector("generic", "marker", ("conformance_generic",)),
            "vmaas": Selector("vmaas", "marker", ("conformance_vmaas",)),
            "storage": Selector("storage", "marker", ("conformance_storage",)),
        },
        profiles={
            "generic": ProfileDefinition("generic", ("generic",)),
            "vm": ProfileDefinition("vm", ("vmaas",)),
            "full": ProfileDefinition("full", ("vmaas", "storage")),
        },
    )
    raw = [_test("TC-1", "conformance_vmaas")]

    enriched = assign_profiles(raw, catalog, ("generic", "vm", "full"))

    assert enriched[0].profiles == ("vm", "full")


def test_catalog_rejects_duplicate_mapping_keys(tmp_path: Path) -> None:
    catalog = tmp_path / "catalog.yaml"
    catalog.write_text(
        "selectors:\n"
        "  - id: generic\n"
        "    type: marker\n"
        "    markers: [conformance_generic]\n"
        "profiles:\n"
        "  - id: generic\n"
        "    selectors: [generic]\n"
        "profiles:\n"
        "  - id: generic\n"
        "    selectors: [generic]\n"
    )

    with pytest.raises(ConfigError, match="duplicate key"):
        load_catalog(catalog)
