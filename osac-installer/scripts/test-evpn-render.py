#!/usr/bin/env python3
"""Verify the EVPN installer values contract renders as expected."""

from __future__ import annotations

import os
import pathlib
import subprocess
import tempfile
from typing import Any

import yaml


ROOT = pathlib.Path(__file__).resolve().parents[1]
DEPS_CHART = ROOT / "charts/osac-deps"
INFRA_CHART = ROOT / "charts/osac-infra"
HOOK_SCRIPT = INFRA_CHART / "files/hooks/configure-evpn.sh"


def render(release: str, chart: pathlib.Path, *args: str) -> list[dict[str, Any]]:
    result = subprocess.run(
        [
            os.environ.get("HELM_BIN", "helm"),
            "template",
            release,
            str(chart),
            "--namespace",
            "osac-infra",
            *args,
        ],
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode:
        raise RuntimeError(f"Helm render failed: {result.stderr}")
    return [
        item
        for item in yaml.safe_load_all(result.stdout)
        if isinstance(item, dict)
    ]


def expect_render_failure(chart: pathlib.Path, values_file: pathlib.Path) -> None:
    result = subprocess.run(
        [
            os.environ.get("HELM_BIN", "helm"),
            "template",
            "osac-infra",
            str(chart),
            "--namespace",
            "osac-infra",
            "--values",
            str(values_file),
        ],
        capture_output=True,
        text=True,
        check=False,
    )
    assert result.returncode != 0, "invalid EVPN values unexpectedly rendered"


def named(
    resources: list[dict[str, Any]], kind: str, name: str
) -> dict[str, Any]:
    matches = [
        item
        for item in resources
        if item.get("kind") == kind and item.get("metadata", {}).get("name") == name
    ]
    assert len(matches) == 1, f"expected one {kind}/{name}, got {len(matches)}"
    return matches[0]


def absent(resources: list[dict[str, Any]], kind: str, name: str) -> None:
    assert not any(
        item.get("kind") == kind and item.get("metadata", {}).get("name") == name
        for item in resources
    ), f"unexpected {kind}/{name} resource"


def config_data(resources: list[dict[str, Any]]) -> dict[str, str]:
    return named(resources, "ConfigMap", "osac-infra-evpn-config")["data"]


def granted_verbs(
    rules: list[dict[str, Any]], api_group: str, resource: str
) -> set[str]:
    return {
        verb
        for rule in rules
        if (api_group in rule["apiGroups"] or "*" in rule["apiGroups"])
        and (resource in rule["resources"] or "*" in rule["resources"])
        for verb in rule["verbs"]
    }


def parse_documents(value: str) -> list[dict[str, Any]]:
    return [item for item in yaml.safe_load_all(value) if isinstance(item, dict)]


def write_values(directory: pathlib.Path, name: str, values: dict[str, Any]) -> pathlib.Path:
    path = directory / name
    path.write_text(yaml.safe_dump(values, sort_keys=False), encoding="utf-8")
    return path


def node(
    name: str,
    vtep_ip: str,
    uplinks: list[dict[str, Any]],
    asn: int | None = None,
) -> dict[str, Any]:
    value: dict[str, Any] = {"name": name, "vtepIP": vtep_ip, "uplinks": uplinks}
    if asn is not None:
        value["asn"] = asn
    return value


def uplink(
    interface: str,
    peer_address: str,
    peer_asn: int = 4200300002,
    address: str | None = None,
) -> dict[str, Any]:
    value: dict[str, Any] = {
        "interface": interface,
        "peerAddress": peer_address,
        "peerASN": peer_asn,
    }
    if address is not None:
        value["address"] = address
    return value


def base_values(nodes: list[dict[str, Any]]) -> dict[str, Any]:
    return {
        "evpn": {
            "enabled": True,
            "nmstate": {"install": True, "channel": "stable"},
            "configureClusterNetwork": True,
            "vtepCIDR": "10.200.255.0/24",
            "bgp": {
                "asn": 4209999999,
                "acceptPrefixes": ["10.200.0.0/16"],
                "waitForEstablished": True,
                "timeoutSeconds": 900,
            },
            "nodes": nodes,
        }
    }


def test_defaults() -> None:
    deps = render("osac-deps", DEPS_CHART)
    infra = render("osac-infra", INFRA_CHART)
    assert not any(
        item.get("metadata", {}).get("namespace") == "openshift-nmstate"
        for item in deps
    ), "disabled EVPN must not render NMState operator resources"
    absent(infra, "ConfigMap", "osac-infra-evpn-config")
    absent(infra, "Job", "osac-infra-configure-evpn")


def test_granted_verbs_aggregates_rules_and_wildcards() -> None:
    rules = [
        {"apiGroups": ["nmstate.io"], "resources": ["nmstates"], "verbs": ["get"]},
        {"apiGroups": ["*"], "resources": ["*"], "verbs": ["watch"]},
    ]
    assert granted_verbs(rules, "nmstate.io", "nmstates") == {"get", "watch"}


def test_readiness_uses_operator_labels() -> None:
    script = HOOK_SCRIPT.read_text(encoding="utf-8")
    assert '-l "app=frr-k8s,component=frr-k8s"' in script
    assert (
        "-l 'operators.coreos.com/kubernetes-nmstate-operator.openshift-nmstate'"
        in script
    )


def test_job_deadline_covers_bgp_wait() -> None:
    values = base_values(
        [node("worker-0", "10.200.255.1", [uplink("ens6", "169.254.133.3")])]
    )
    values["evpn"]["bgp"]["timeoutSeconds"] = 3600
    with tempfile.TemporaryDirectory(prefix="evpn-deadline-") as temp:
        values_file = write_values(pathlib.Path(temp), "deadline.yaml", values)
        infra = render("osac-infra", INFRA_CHART, "--values", str(values_file))

    job = named(infra, "Job", "osac-infra-configure-evpn")
    assert job["spec"]["activeDeadlineSeconds"] == 7200


def test_prune_listing_failure_is_fatal() -> None:
    with tempfile.TemporaryDirectory(prefix="evpn-prune-") as temp:
        directory = pathlib.Path(temp)
        config_dir = directory / "config"
        bin_dir = directory / "bin"
        config_dir.mkdir()
        bin_dir.mkdir()

        (config_dir / "settings.env").write_text(
            "CONFIGURE_CLUSTER_NETWORK=false\n"
            "WAIT_FOR_ESTABLISHED=false\n"
            "BGP_TIMEOUT_SECONDS=60\n",
            encoding="utf-8",
        )
        (config_dir / "nodes.txt").write_text("worker-0\n", encoding="utf-8")
        (config_dir / "peers.txt").write_text("", encoding="utf-8")
        for filename in (
            "cluster-network-patch.json",
            "nmstate.yaml",
            "nncp.yaml",
            "vtep.yaml",
            "frr.yaml",
            "routeadvertisements.yaml",
        ):
            (config_dir / filename).write_text("", encoding="utf-8")

        oc_log = directory / "oc.log"
        mock_oc = bin_dir / "oc"
        mock_oc.write_text(
            "#!/usr/bin/env bash\n"
            "set -euo pipefail\n"
            "printf '%s\\n' \"$*\" >> \"${MOCK_OC_LOG}\"\n"
            "case \"${1:-} ${2:-}\" in\n"
            "  'get network.operator.openshift.io') echo OVNKubernetes ;;\n"
            "  'get pods') echo True ;;\n"
            "  'get csv') echo Succeeded ;;\n"
            "  'get vtep.k8s.ovn.org')\n"
            "    if [[ \"${5:-}\" == *reason* ]]; then echo Allocated; else echo True; fi\n"
            "    ;;\n"
            "  'get nncp')\n"
            "    if [[ \"${4:-}\" == osac.openshift.io/evpn=true ]]; then\n"
            "      echo nncp/osac-evpn-stale\n"
            "      echo 'simulated list failure' >&2\n"
            "      exit 1\n"
            "    fi\n"
            "    ;;\n"
            "esac\n",
            encoding="utf-8",
        )
        mock_oc.chmod(0o755)

        env = os.environ.copy()
        env["CONFIG_DIR"] = str(config_dir)
        env["MOCK_OC_LOG"] = str(oc_log)
        env["PATH"] = f"{bin_dir}{os.pathsep}{env['PATH']}"
        result = subprocess.run(
            ["bash", str(HOOK_SCRIPT)],
            capture_output=True,
            text=True,
            env=env,
            check=False,
            timeout=30,
        )

        calls = oc_log.read_text(encoding="utf-8").splitlines()
        assert result.returncode != 0, "hook must fail when pruning list fails"
        assert "Failed to list NNCP resources for pruning" in result.stderr, (
            result.stderr
        )
        assert not any(call.startswith("delete ") for call in calls)


def test_single_node() -> None:
    values = base_values(
        [
            node(
                "worker-0",
                "10.200.255.1",
                [
                    uplink(
                        "ens6",
                        "169.254.133.3",
                        address="169.254.133.2/31",
                    )
                ],
            )
        ]
    )
    with tempfile.TemporaryDirectory(prefix="evpn-values-") as temp:
        values_file = write_values(pathlib.Path(temp), "single.yaml", values)
        deps = render("osac-deps", DEPS_CHART, "--values", str(values_file))
        infra = render("osac-infra", INFRA_CHART, "--values", str(values_file))

    subscription = named(deps, "Subscription", "kubernetes-nmstate-operator")
    assert subscription["metadata"]["namespace"] == "openshift-nmstate"
    named(infra, "ConfigMap", "osac-infra-evpn-config")
    job = named(infra, "Job", "osac-infra-configure-evpn")
    assert job["metadata"]["annotations"]["helm.sh/hook-weight"] == "30"
    frr_namespace = named(infra, "Namespace", "openshift-frr-k8s")
    assert frr_namespace["metadata"]["annotations"][
        "helm.sh/resource-policy"
    ] == "keep"
    for kind in ("ServiceAccount", "ClusterRole", "ClusterRoleBinding"):
        resource = named(infra, kind, "osac-infra-configure-evpn")
        annotations = resource["metadata"].get("annotations", {})
        assert annotations.get("helm.sh/hook-weight") == "30"
        assert "helm.sh/hook" not in annotations, (
            f"{kind} must be Helm-managed so uninstall removes the EVPN permissions"
        )

    scoped_rbac = (
        ("Role", "osac-infra-configure-evpn-nmstate", "openshift-nmstate"),
        ("RoleBinding", "osac-infra-configure-evpn-nmstate", "openshift-nmstate"),
        ("Role", "osac-infra-configure-evpn-frr", "openshift-frr-k8s"),
        ("RoleBinding", "osac-infra-configure-evpn-frr", "openshift-frr-k8s"),
    )
    for kind, name, namespace in scoped_rbac:
        resource = named(infra, kind, name)
        assert resource["metadata"]["namespace"] == namespace
        annotations = resource["metadata"].get("annotations", {})
        assert annotations.get("helm.sh/hook-weight") == "30"
        assert "helm.sh/hook" not in annotations, (
            f"{kind}/{name} must be Helm-managed for uninstall cleanup"
        )

    cluster_role = named(infra, "ClusterRole", "osac-infra-configure-evpn")
    cluster_rules = cluster_role["rules"]
    assert granted_verbs(
        cluster_rules, "apiextensions.k8s.io", "customresourcedefinitions"
    ) == {"get", "list", "watch"}
    assert granted_verbs(cluster_rules, "nmstate.io", "nmstates") == {
        "get", "list", "watch", "create", "patch"
    }
    assert granted_verbs(
        cluster_rules, "nmstate.io", "nodenetworkconfigurationpolicies"
    ) == {"get", "list", "watch", "create", "patch", "delete"}

    for api_group, resource in (
        ("", "pods"),
        ("operators.coreos.com", "clusterserviceversions"),
        ("frrk8s.metallb.io", "frrconfigurations"),
        ("frrk8s.metallb.io", "bgpsessionstates"),
    ):
        assert not granted_verbs(cluster_rules, api_group, resource), (
            f"{resource} permissions must be namespaced"
        )

    for api_group, resource in (
        ("", "nodes"),
        ("k8s.ovn.org", "vteps"),
        ("k8s.ovn.org", "routeadvertisements"),
    ):
        assert "list" not in granted_verbs(cluster_rules, api_group, resource), (
            f"the hook must not list unrelated {resource} resources"
        )

    data = config_data(infra)
    nncp = named(parse_documents(data["nncp.yaml"]), "NodeNetworkConfigurationPolicy", "osac-evpn-worker-0")
    interfaces = nncp["spec"]["desiredState"]["interfaces"]
    ethernet = next(interface for interface in interfaces if interface["name"] == "ens6")
    assert ethernet["ipv4"]["address"] == [{"ip": "169.254.133.2", "prefix-length": 31}]
    vtep_interface = next(interface for interface in interfaces if interface["name"] == "evpn-vtep0")
    assert vtep_interface["ipv4"]["address"] == [{"ip": "10.200.255.1", "prefix-length": 32}]

    vtep = named(parse_documents(data["vtep.yaml"]), "VTEP", "tenant-vtep")
    assert vtep["spec"]["cidrs"] == ["10.200.255.0/24"]
    route_advertisements = named(
        parse_documents(data["routeadvertisements.yaml"]), "RouteAdvertisements", "osac-evpn"
    )
    selector = route_advertisements["spec"]["networkSelectors"][0][
        "clusterUserDefinedNetworkSelector"
    ]["networkSelector"]["matchLabels"]
    assert selector == {"evpn": "true"}

    frr = parse_documents(data["frr.yaml"])
    underlay = named(frr, "FRRConfiguration", "osac-evpn-worker-0")
    router = underlay["spec"]["bgp"]["routers"][0]
    assert router["asn"] == 4209999999 and isinstance(router["asn"], int)
    assert "sourceaddress" in router["neighbors"][0]
    assert "e+09" not in str(infra), "large ASNs must not render as float notation"
    assert data["peers.txt"].splitlines() == ["worker-0 169.254.133.3"]


def test_multiple_nodes() -> None:
    values = base_values(
        [
            node("worker-0", "10.200.255.1", [uplink("ens6", "169.254.133.1")]),
            node(
                "worker-1",
                "10.200.255.2",
                [uplink("ens6", "169.254.134.1"), uplink("ens7", "169.254.135.1")],
                asn=4209999998,
            ),
        ]
    )
    with tempfile.TemporaryDirectory(prefix="evpn-values-") as temp:
        values_file = write_values(pathlib.Path(temp), "multiple.yaml", values)
        infra = render("osac-infra", INFRA_CHART, "--values", str(values_file))

    job = named(infra, "Job", "osac-infra-configure-evpn")
    assert job["spec"]["activeDeadlineSeconds"] == 6300

    data = config_data(infra)
    nncps = parse_documents(data["nncp.yaml"])
    assert {item["metadata"]["name"] for item in nncps} == {
        "osac-evpn-worker-0",
        "osac-evpn-worker-1",
    }
    frr = parse_documents(data["frr.yaml"])
    second = named(frr, "FRRConfiguration", "osac-evpn-worker-1")
    assert second["spec"]["bgp"]["routers"][0]["asn"] == 4209999998
    raw = named(frr, "FRRConfiguration", "osac-evpn-raw-worker-1")
    raw_config = raw["spec"]["raw"]["rawConfig"]
    assert "neighbor 169.254.134.1 activate" in raw_config
    assert "neighbor 169.254.135.1 activate" in raw_config
    assert len(data["peers.txt"].splitlines()) == 3


def test_uplink_without_address() -> None:
    values = base_values([node("worker-0", "10.200.255.1", [uplink("ens6", "169.254.133.1")])])
    with tempfile.TemporaryDirectory(prefix="evpn-values-") as temp:
        values_file = write_values(pathlib.Path(temp), "no-address.yaml", values)
        infra = render("osac-infra", INFRA_CHART, "--values", str(values_file))

    data = config_data(infra)
    nncp = named(parse_documents(data["nncp.yaml"]), "NodeNetworkConfigurationPolicy", "osac-evpn-worker-0")
    interfaces = nncp["spec"]["desiredState"]["interfaces"]
    assert all(interface["name"] != "ens6" for interface in interfaces)
    underlay = named(parse_documents(data["frr.yaml"]), "FRRConfiguration", "osac-evpn-worker-0")
    assert "sourceaddress" not in underlay["spec"]["bgp"]["routers"][0]["neighbors"][0]


def test_nmstate_install_disabled() -> None:
    values = base_values([node("worker-0", "10.200.255.1", [uplink("ens6", "169.254.133.1")])])
    values["evpn"]["nmstate"]["install"] = False
    with tempfile.TemporaryDirectory(prefix="evpn-values-") as temp:
        values_file = write_values(pathlib.Path(temp), "no-subscription.yaml", values)
        deps = render("osac-deps", DEPS_CHART, "--values", str(values_file))
        infra = render("osac-infra", INFRA_CHART, "--values", str(values_file))

    absent(deps, "Subscription", "kubernetes-nmstate-operator")
    named(infra, "Job", "osac-infra-configure-evpn")


def test_invalid_values() -> None:
    valid_node = node("worker-0", "10.200.255.1", [uplink("ens6", "169.254.133.1")])
    invalid: list[dict[str, Any]] = []

    empty_vtep = base_values([valid_node])
    empty_vtep["evpn"]["vtepCIDR"] = ""
    invalid.append(empty_vtep)

    no_nodes = base_values([])
    invalid.append(no_nodes)

    duplicate_vtep = base_values(
        [
            valid_node,
            node("worker-1", "10.200.255.1", [uplink("ens7", "169.254.134.1")]),
        ]
    )
    invalid.append(duplicate_vtep)

    duplicate_name = base_values(
        [
            valid_node,
            node("worker-0", "10.200.255.2", [uplink("ens7", "169.254.134.1")]),
        ]
    )
    invalid.append(duplicate_name)

    duplicate_peer = base_values(
        [
            node(
                "worker-0",
                "10.200.255.1",
                [uplink("ens6", "169.254.133.1"), uplink("ens7", "169.254.133.1")],
            )
        ]
    )
    invalid.append(duplicate_peer)

    invalid_asn = base_values([valid_node])
    invalid_asn["evpn"]["bgp"]["asn"] = 4294967295
    invalid.append(invalid_asn)

    missing_peer_asn = base_values(
        [node("worker-0", "10.200.255.1", [{"interface": "ens6", "peerAddress": "169.254.133.1"}])]
    )
    invalid.append(missing_peer_asn)

    with tempfile.TemporaryDirectory(prefix="evpn-invalid-") as temp:
        directory = pathlib.Path(temp)
        for index, values in enumerate(invalid):
            values_file = write_values(directory, f"invalid-{index}.yaml", values)
            expect_render_failure(INFRA_CHART, values_file)


def main() -> None:
    test_defaults()
    test_granted_verbs_aggregates_rules_and_wildcards()
    test_readiness_uses_operator_labels()
    test_job_deadline_covers_bgp_wait()
    test_prune_listing_failure_is_fatal()
    test_single_node()
    test_multiple_nodes()
    test_uplink_without_address()
    test_nmstate_install_disabled()
    test_invalid_values()
    print("EVPN host networking Helm render contract passed.")


if __name__ == "__main__":
    main()
