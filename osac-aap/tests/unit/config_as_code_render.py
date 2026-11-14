"""Shared rendering helpers for config-as-code pod-spec tests."""

from pathlib import Path

import yaml
from ansible.parsing.dataloader import DataLoader
from ansible.template import Templar

CONFIG_AS_CODE = (
    Path(__file__).parents[2] / "collections" / "ansible_collections" / "osac" / "config_as_code"
)
CONFIG_VARS = CONFIG_AS_CODE / "playbooks" / "vars" / "config.yml"
NETRIS_ENV = ("NETRIS_CONTROLLER_URL", "NETRIS_VALIDATE_CERTS", "NETRIS_CA_CONFIGMAP")


def render_pod(monkeypatch, template, environment):
    """Render a container-group pod spec with config-as-code's variables."""
    for name in NETRIS_ENV:
        monkeypatch.delenv(name, raising=False)
    for name, value in environment.items():
        monkeypatch.setenv(name, value)
    variables = yaml.safe_load(CONFIG_VARS.read_text())
    variables["aap_ee_image"] = "example.invalid/osac-ee:test"
    templar = Templar(loader=DataLoader(), variables=variables)
    pod = yaml.safe_load(templar.template(template.read_text()))
    worker = pod["spec"]["containers"][0]
    env = {item["name"]: item.get("value") for item in worker.get("env", [])}
    return pod, worker, env
