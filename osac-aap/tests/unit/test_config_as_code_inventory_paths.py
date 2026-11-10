"""SCM inventory paths in the monorepo-backed AAP project."""

from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[3]
CONFIG = ROOT / "osac-aap/collections/ansible_collections/osac/config_as_code"


def test_inventory_sources_resolve_from_the_project_root() -> None:
    """Require each shared inventory source to resolve to the localhost inventory."""
    objects = yaml.safe_load((CONFIG / "roles/aap/vars/controller.yml").read_text())
    sources = objects["controller_inventory_sources"]
    assert sources
    for source in sources:
        inventory = yaml.safe_load((ROOT / source["source_path"]).read_text())
        assert "localhost" in inventory["all"]["hosts"]
