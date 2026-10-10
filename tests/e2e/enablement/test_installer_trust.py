"""Deployed installer hook and AAP publishing verification for OSAC-5547."""

from __future__ import annotations

import json
import os
import re

import pytest
import yaml

from tests.e2e.core.runner import run

pytestmark = pytest.mark.regression


def _has_insecure_curl(script: str) -> bool:
    curl_commands = re.finditer(r"\bcurl\b(?P<args>(?:\\\r?\n|[^\n])*)", script)
    insecure_option = re.compile(r"(?:^|\s)(?:--insecure|-[A-Za-z]*k[A-Za-z]*)(?=\s|$)")
    return any(insecure_option.search(match.group("args")) for match in curl_commands)


@pytest.mark.parametrize(
    "script",
    [
        "curl -k https://example.test",
        "curl --insecure https://example.test",
        "curl -sk https://example.test",
        "curl --silent \\\n          --insecure https://example.test",
    ],
)
def test_insecure_curl_options_are_detected(script: str) -> None:
    assert _has_insecure_curl(script)


@pytest.mark.parametrize(
    "script",
    [
        "curl --cacert /etc/ca.pem https://example.test",
        "echo --insecure",
        "grpcurl -insecure example.test:443 service.Method",
    ],
)
def test_secure_or_non_curl_commands_are_not_flagged(script: str) -> None:
    assert not _has_insecure_curl(script)


def test_fulfillment_hooks_and_template_publishing(namespace: str) -> None:
    release = os.environ.get("OSAC_HELM_RELEASE", "osac")
    status = json.loads(run("helm", "status", release, "-n", namespace, "-o", "json"))
    assert status["info"]["status"] == "deployed", "Installer hooks must have completed successfully"

    hooks = run("helm", "get", "hooks", release, "-n", namespace)
    hook_jobs = {
        document["metadata"]["name"]: document
        for document in yaml.safe_load_all(hooks)
        if isinstance(document, dict) and document.get("kind") == "Job" and document.get("metadata", {}).get("name")
    }
    required_hooks = ("fulfillment-create-hub", "create-network-class")
    optional_hooks = ("seed-cluster-versions", "register-local-storage", "osac-publish-templates")
    for name in (*required_hooks, *optional_hooks):
        job = hook_jobs.get(name)
        if job is None:
            assert name not in required_hooks, f"Missing installer hook {name}"
            continue
        pod = job["spec"]["template"]["spec"]
        containers = [*pod.get("initContainers", []), *pod["containers"]]
        script = "\n".join(
            str(value)
            for container in containers
            for value in (*container.get("command", []), *container.get("args", []))
        )
        assert "--cacert /etc/ca-bundle/bundle.pem" in script, name
        ca_volumes = {
            volume["name"]
            for volume in pod.get("volumes", [])
            if volume.get("configMap", {}).get("name") == "ca-bundle"
        }
        assert ca_volumes, name
        assert any(
            mount.get("name") in ca_volumes and mount.get("mountPath") == "/etc/ca-bundle"
            for container in containers
            for mount in container.get("volumeMounts", [])
        ), name
        assert not _has_insecure_curl(script), name
