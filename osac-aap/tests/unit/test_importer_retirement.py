"""The retired pre-boot inventory importers must not be advertised or scheduled."""

import json
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[3]
AAP = ROOT / "osac-aap"
INSTALLER = ROOT / "osac-installer" / "charts" / "osac"


def test_importer_jobs_and_schedules_are_absent():
    controller = (AAP / "collections/ansible_collections/osac/config_as_code/roles/aap/vars/controller.yml").read_text()
    assert '"{{ aap_prefix }}-import-agents"' not in controller
    assert '"{{ aap_prefix }}-import-bcm-agents"' not in controller
    assert "import-agents-inventory" not in controller
    assert "bcm-certs" not in controller
    # Keep the on-demand BMI provisioning job templates.
    assert '"{{ aap_prefix }}-create-bare-metal-instance"' in controller


def test_installer_does_not_offer_agent_pool_import_values():
    schema = json.loads((INSTALLER / "values.schema.json").read_text())
    values = yaml.safe_load((INSTALLER / "values.yaml").read_text())
    for config in (schema["properties"]["aap"]["properties"], values["aap"]):
        for name in ("importAgents", "importBcmAgents"):
            assert name not in config
        config_as_code = config["properties"]["configAsCode"]["properties"] if "properties" in config else config["configAsCode"]
        for name in ("importAgentsEnabled", "importBcmAgentsEnabled"):
            assert name not in config_as_code
    assert "bmf" in values  # BMaaS remains a distinct service.


def test_import_only_artifacts_removed_without_touching_bmaas():
    for path in (
        AAP / "playbook_osac_import_agents.yml",
        AAP / "playbook_osac_import_bcm_agents.yml",
        AAP / "group_vars/all/bcm.yaml",
        AAP / "collections/ansible_collections/osac/service/plugins/filter/bcm.py",
        AAP / "charts/aap/templates/import-agents-inventory.yaml",
        AAP / "charts/aap/templates/bcm-certs.yaml",
    ):
        assert not path.exists(), f"retired importer remains: {path}"
    assert (AAP / "collections/ansible_collections/osac/service/plugins/filter/agents.py").exists()
    assert (ROOT / "bare-metal-fulfillment-operator/internal/inventory/bcm.go").exists()
