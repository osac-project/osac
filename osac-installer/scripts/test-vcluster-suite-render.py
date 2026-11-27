#!/usr/bin/env python3
"""Check the suite-selection overlay on the vcluster-ci profile.

One profile serves all three E2E suites: the per-run overlay the run setup
(osac-test-infra scripts/vcluster-run-setup.sh) writes selects the suite,
with the keys documented in values/vcluster-ci/instance.yaml. This script
renders the profile with each suite's overlay, exactly as the run's
`make install-osac` invocation does (REMOTE_KUBECONFIG is required for the
profile, so the remote-cluster Helm arguments are always present), and
checks that the suite's objects appear and the other suites' disappear.

The VMaaS overlay must render byte-identically to no overlay at all: the
profile's default is VMaaS, and the overlay for a VMaaS run only restates
that default.

Run: uv run --project .. python scripts/test-vcluster-suite-render.py
"""

from __future__ import annotations

import pathlib
import re
import subprocess
import tempfile
from typing import Any

import yaml

INSTALLER = pathlib.Path(__file__).resolve().parents[1]
OSAC_CHART = INSTALLER / "charts/osac"
PROFILE = INSTALLER / "values/vcluster-ci/instance.yaml"

CLUSTER_DOMAIN = "example.com"
AAP_CONTROLLER_URL = "https://aap.example.com"
AAP_API_URL = f"{AAP_CONTROLLER_URL}/api/controller"
HOSTED_BASE_DOMAIN = "hosted.example.com"

# The remote-cluster Helm arguments install-osac passes with
# REMOTE_KUBECONFIG set (REMOTE_CLUSTER_HELM_ARGS in the Makefile); a
# vcluster-ci install always has them.
REMOTE_ARGS = [
    "--set-string", "operator.remoteClusterKubeconfig=/var/run/secrets/remote/kubeconfig",
    "--set-string", "operator.remoteClusterKubeconfigSecretName=osac-remote-kubeconfig",
    "--set-string", "operator.remoteClusterKubeconfigSecretKey=kubeconfig",
    "--set-string", "aap.configAsCode.remoteClusterKubeconfigSecretName=osac-remote-kubeconfig",
    "--set-string", "aap.configAsCode.remoteClusterKubeconfigSecretKey=kubeconfig",
    "--set-string", "bmf.remoteClusterKubeconfig=/var/run/secrets/remote/kubeconfig",
    "--set-string", "bmf.remoteClusterKubeconfigSecretName=osac-remote-kubeconfig",
    "--set-string", "bmf.remoteClusterKubeconfigSecretKey=kubeconfig",
]

# The run-ID keys every overlay carries (the ones that interact with the
# suite keys), with the suite blocks from values/vcluster-ci/instance.yaml.
VMAAS_OVERLAY: dict[str, Any] = {
    "global": {
        "instanceId": "pr2100v1",
        "services": {"vmaas": {"enabled": True}},
    },
}

BMF_KEYS: dict[str, Any] = {
    "bmf": {
        "metal3": {"enabled": True, "namespace": "HOSTS_NAMESPACE"},
        "env": {
            "aapUrl": AAP_API_URL,
            "aapInsecureSkipVerify": "true",
            "aapTemplatePrefix": "INSTANCE_ID",
        },
        "secretAPI": {
            "issuerUrl": f"https://keycloak-keycloak.{CLUSTER_DOMAIN}/realms/INSTANCE_ID",
            "credentialsSecret": "INSTANCE_ID-fulfillment-controller-credentials",
        },
    },
}

BMAAS_OVERLAY: dict[str, Any] = {
    "global": {
        "instanceId": "pr2100b1",
        "services": {"vmaas": {"enabled": False}, "bmaas": {"enabled": True}},
        "networking": {"k8sManager": ""},
    },
    "operator": {"aap": {"url": AAP_API_URL, "templatePrefix": "pr2100b1"}},
    "aap": {"instanceGroups": {"clusterFulfillment": {"enabled": True}}},
    **BMF_KEYS,
}

CAAS_OVERLAY: dict[str, Any] = {
    "global": {
        "instanceId": "pr2100c1",
        "services": {
            "caas": {"enabled": True},
            "bmaas": {"enabled": True},
            "vmaas": {"enabled": True},
        },
        "networking": {"fabricManager": "agentless_net"},
    },
    "operator": {
        "aap": {
            "url": AAP_API_URL,
            "organization": "pr2100c1",
            "templatePrefix": "pr2100c1",
        },
        "controllers": {"computeInstance": False},
        "ignition": {"trustIngressCA": True},
    },
    "aap": {
        "instanceGroups": {
            "clusterFulfillment": {
                "enabled": True,
                "config": {
                    "HOSTED_CLUSTER_CONTROLLER_AVAILABILITY_POLICY": "SingleReplica",
                    "HOSTED_CLUSTER_INFRASTRUCTURE_AVAILABILITY_POLICY": "SingleReplica",
                    "HOSTED_CLUSTER_BASE_DOMAIN": HOSTED_BASE_DOMAIN,
                },
            }
        }
    },
    "clusterVersions": {
        "enabled": True,
        "versions": [
            {"version": "4.20.0", "image": "quay.io/openshift-release-dev/ocp-release:4.20.0-multi"},
            {
                "version": "4.22.0",
                "image": "quay.io/openshift-release-dev/ocp-release:4.22.0-multi",
                "default": True,
                "diskImage": {
                    "name": "rhcos-4-22",
                    "sourceRef": "oci://quay.io/osac-project/rhcos:4.22.0",
                },
            },
        ],
    },
    **BMF_KEYS,
}


def fill_template(overlay: dict[str, Any], run_id: str) -> dict[str, Any]:
    """Resolve the INSTANCE_ID/HOSTS_NAMESPACE placeholders in a copied overlay."""
    text = yaml.safe_dump(overlay).replace("INSTANCE_ID", run_id).replace(
        "HOSTS_NAMESPACE", f"{run_id}-hosts"
    )
    result = yaml.safe_load(text)
    assert isinstance(result, dict)
    return result


def render_raw(overlay: dict[str, Any] | None) -> str:
    """Render the profile the way a run's install-osac does, plus an overlay."""
    command = [
        "helm",
        "template",
        "osac",
        str(OSAC_CHART),
        "--values",
        str(OSAC_CHART / "ci/default-values.yaml"),
        "--values",
        str(PROFILE),
        "--set-string",
        f"global.clusterDomain={CLUSTER_DOMAIN}",
        "--set",
        "service.externalHostname=fulfillment.example.com",
        "--set",
        "service.internalHostname=fulfillment-internal.example.com",
        "--set",
        f"aap.controllerUrl={AAP_CONTROLLER_URL}",
        *REMOTE_ARGS,
    ]
    if overlay is None:
        result = subprocess.run(command, check=False, capture_output=True, text=True)
        if result.returncode:
            raise RuntimeError(f"Helm render failed: {' '.join(command)}\n{result.stderr}")
        return result.stdout
    with tempfile.TemporaryDirectory(prefix="vcluster-suite-overlay-") as temp_dir:
        overlay_path = pathlib.Path(temp_dir) / "overlay.yaml"
        overlay_path.write_text(yaml.safe_dump(overlay), encoding="utf-8")
        result = subprocess.run(
            [*command, "--values", str(overlay_path)],
            check=False,
            capture_output=True,
            text=True,
        )
        if result.returncode:
            raise RuntimeError(f"Helm render failed: {' '.join(command)}\n{result.stderr}")
        return result.stdout


def docs_of(manifest: str) -> list[dict[str, Any]]:
    return [doc for doc in yaml.safe_load_all(manifest) if isinstance(doc, dict)]


def find(docs: list[dict[str, Any]], kind: str, name: str) -> dict[str, Any] | None:
    matches = [
        doc
        for doc in docs
        if doc.get("kind") == kind and doc.get("metadata", {}).get("name") == name
    ]
    assert len(matches) <= 1, f"expected at most one {kind}/{name}, found {len(matches)}"
    return matches[0] if matches else None


def require(docs: list[dict[str, Any]], kind: str, name: str) -> dict[str, Any]:
    doc = find(docs, kind, name)
    assert doc is not None, f"{kind}/{name} is missing from the render"
    return doc


def container_of(doc: dict[str, Any], name: str) -> dict[str, Any]:
    for container in doc["spec"]["template"]["spec"]["containers"]:
        if container["name"] == name:
            return container
    raise AssertionError(f"no {name} container in {doc['metadata']['name']}")


def env_of(doc: dict[str, Any], container: str, variable: str) -> str:
    for entry in container_of(doc, container).get("env", []):
        if entry["name"] == variable:
            return entry["value"]
    raise AssertionError(f"{variable} is not set on the {container} container")


def arg_of(doc: dict[str, Any], container: str, flag: str) -> str:
    command = container_of(doc, container).get("command", [])
    for part in command:
        if part.startswith(flag + "="):
            return part.split("=", 1)[1]
    raise AssertionError(f"{flag} is not passed to the {container} container")


def command_text(doc: dict[str, Any], container: str) -> str:
    return "\n".join(container_of(doc, container).get("command", []))


def check_vmaas_suite_key_is_byte_identical() -> None:
    """The VMaaS suite key restates the profile default, so it renders the same."""
    def normalized(manifest: str) -> str:
        return re.sub(r'"generatedAt":"[^"]*"', '"generatedAt":"X"', manifest)

    assert normalized(render_raw(None)) == normalized(
        render_raw({"global": {"services": {"vmaas": {"enabled": True}}}})
    ), "the VMaaS service key must not change the vcluster-ci render"


def check_vmaas_shape(manifest: str) -> None:
    docs = docs_of(manifest)
    assert find(docs, "Deployment", "bmf-operator-controller-manager") is None, (
        "the BMF operator deploys without the bmaas service key"
    )
    assert find(docs, "Job", "seed-cluster-versions") is None, (
        "cluster versions seed without the caas clusterVersions key"
    )
    operator = require(docs, "Deployment", "osac-operator")
    assert env_of(operator, "manager", "OSAC_ENABLE_COMPUTE_INSTANCE_CONTROLLER") == "true"
    assert env_of(operator, "manager", "OSAC_ENABLE_CLUSTER_CONTROLLER") == "false"
    assert env_of(operator, "manager", "OSAC_ENABLE_BAREMETAL_INSTANCE_CONTROLLER") == "false"
    require(docs, "ConfigMap", "osac-network-k8s-manager-cudn-evpn")
    require(docs, "ConfigMap", "osac-network-fabric-manager-netris")
    assert find(docs, "ConfigMap", "osac-network-fabric-manager-agentless-net") is None


def check_bmf_wiring(docs: list[dict[str, Any]], run_id: str) -> None:
    """The BMaaS keys reach the BMF operator whatever suite asked for them."""
    deployment = require(docs, "Deployment", "bmf-operator-controller-manager")
    assert env_of(deployment, "manager", "OSAC_AAP_URL") == AAP_API_URL
    assert env_of(deployment, "manager", "OSAC_AAP_TEMPLATE_PREFIX") == run_id
    assert env_of(deployment, "manager", "OSAC_AAP_INSECURE_SKIP_VERIFY") == "true"
    assert env_of(deployment, "manager", "OSAC_REMOTE_CLUSTER_KUBECONFIG") == (
        "/var/run/secrets/remote/kubeconfig"
    )
    volumes = {
        volume["name"]: volume for volume in deployment["spec"]["template"]["spec"]["volumes"]
    }
    assert (
        volumes["secret-api-client-credentials"]["secret"]["secretName"]
        == f"{run_id}-fulfillment-controller-credentials"
    )
    assert volumes["remote-kubeconfig"]["secret"]["secretName"] == "osac-remote-kubeconfig"
    inventory = require(docs, "Secret", "osac-inventory-config")
    assert f'namespace: "{run_id}-hosts"' in inventory["stringData"]["inventory.yaml"]


def check_bmaas_overlay() -> None:
    docs = docs_of(render_raw(fill_template(BMAAS_OVERLAY, "pr2100b1")))
    check_bmf_wiring(docs, "pr2100b1")

    grpc = require(docs, "Deployment", "fulfillment-grpc-server")
    assert arg_of(grpc, "grpc-server", "--enable-vmaas") == "false"
    assert arg_of(grpc, "grpc-server", "--enable-bmaas") == "true"
    assert arg_of(grpc, "grpc-server", "--enable-caas") == "false"

    operator = require(docs, "Deployment", "osac-operator")
    assert env_of(operator, "manager", "OSAC_ENABLE_BAREMETAL_INSTANCE_CONTROLLER") == "true"
    assert env_of(operator, "manager", "OSAC_ENABLE_COMPUTE_INSTANCE_CONTROLLER") == "false"
    assert env_of(operator, "manager", "OSAC_AAP_URL") == AAP_API_URL

    # No Kubernetes Manager: BMaaS SecurityGroups keep their ICMP rules.
    assert find(docs, "ConfigMap", "osac-network-k8s-manager-cudn-evpn") is None
    require(docs, "ConfigMap", "osac-network-fabric-manager-netris")
    assert find(docs, "Job", "seed-cluster-versions") is None


def check_caas_overlay() -> None:
    run_id = "pr2100c1"
    docs = docs_of(render_raw(fill_template(CAAS_OVERLAY, run_id)))
    check_bmf_wiring(docs, run_id)

    require(docs, "Job", "seed-cluster-versions")

    grpc = require(docs, "Deployment", "fulfillment-grpc-server")
    assert arg_of(grpc, "grpc-server", "--enable-vmaas") == "true"
    assert arg_of(grpc, "grpc-server", "--enable-bmaas") == "true"
    assert arg_of(grpc, "grpc-server", "--enable-caas") == "true"

    operator = require(docs, "Deployment", "osac-operator")
    assert env_of(operator, "manager", "OSAC_ENABLE_CLUSTER_CONTROLLER") == "true"
    # vmaas stays on for the DiskImages API, but provisions no VMs.
    assert env_of(operator, "manager", "OSAC_ENABLE_COMPUTE_INSTANCE_CONTROLLER") == "false"
    assert env_of(operator, "manager", "OSAC_ENABLE_BAREMETAL_INSTANCE_CONTROLLER") == "true"
    assert env_of(operator, "manager", "OSAC_IGNITION_TRUST_INGRESS_CA") == "true"

    # agentless_net replaces netris as the fabric manager; the K8s manager
    # stays (CaaS keeps the VM overlay networking shape).
    require(docs, "ConfigMap", "osac-network-fabric-manager-agentless-net")
    assert find(docs, "ConfigMap", "osac-network-fabric-manager-netris") is None
    require(docs, "ConfigMap", "osac-network-k8s-manager-cudn-evpn")

    # The vcluster has no ingress API, so the chart's lookup default cannot
    # derive the hosted base domain: the overlay must carry it.
    config = require(docs, "ConfigMap", "cluster-fulfillment-ig")["data"]
    assert config["HOSTED_CLUSTER_BASE_DOMAIN"] == HOSTED_BASE_DOMAIN
    assert (
        config["HOSTED_CLUSTER_CONTROLLER_AVAILABILITY_POLICY"] == "SingleReplica"
    )
    assert (
        config["HOSTED_CLUSTER_INFRASTRUCTURE_AVAILABILITY_POLICY"] == "SingleReplica"
    )

    # The run's AAP organization and template prefix: the cluster-order
    # templates exist only as <run id>-create-hosted-cluster and
    # <run id>-delete-hosted-cluster in that organization, and the publish
    # hook looks its own template up by name first. Unset, both fall back to
    # the outer install's shared "osac" organization and templates.
    assert env_of(operator, "manager", "OSAC_AAP_ORGANIZATION") == run_id
    assert env_of(operator, "manager", "OSAC_AAP_TEMPLATE_PREFIX") == run_id
    publish = require(docs, "Job", "osac-publish-templates")
    assert env_of(publish, "publish-templates", "OSAC_AAP_TEMPLATE_PREFIX") == run_id
    assert f'TEMPLATE_NAME="{run_id}-publish-templates"' in command_text(
        publish, "publish-templates"
    )


def main() -> None:
    check_vmaas_suite_key_is_byte_identical()
    check_vmaas_shape(render_raw(fill_template(VMAAS_OVERLAY, "pr2100v1")))
    check_bmaas_overlay()
    check_caas_overlay()
    print("vcluster suite-overlay render checks passed.")


if __name__ == "__main__":
    main()
