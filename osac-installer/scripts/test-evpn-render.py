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
    """Render a chart with helm template and return its objects."""
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
    """Assert that the chart rejects the supplied values."""
    result = render_failure_result(chart, values_file)
    assert result.returncode != 0, "invalid EVPN values unexpectedly rendered"


def render_failure_result(
    chart: pathlib.Path, values_file: pathlib.Path
) -> subprocess.CompletedProcess[str]:
    """Run helm template and return the failed result."""
    return subprocess.run(
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


def named(
    resources: list[dict[str, Any]], kind: str, name: str
) -> dict[str, Any]:
    """Return the single rendered resource with the given kind and name."""
    matches = [
        item
        for item in resources
        if item.get("kind") == kind and item.get("metadata", {}).get("name") == name
    ]
    assert len(matches) == 1, f"expected one {kind}/{name}, got {len(matches)}"
    return matches[0]


def absent(resources: list[dict[str, Any]], kind: str, name: str) -> None:
    """Assert that the given resource was not rendered."""
    assert not any(
        item.get("kind") == kind and item.get("metadata", {}).get("name") == name
        for item in resources
    ), f"unexpected {kind}/{name} resource"


def config_data(resources: list[dict[str, Any]]) -> dict[str, str]:
    """Return the EVPN configuration ConfigMap data."""
    return named(resources, "ConfigMap", "osac-infra-evpn-config")["data"]


def granted_verbs(
    rules: list[dict[str, Any]], api_group: str, resource: str
) -> set[str]:
    """Return the verbs granted for an API group and resource."""
    return {
        verb
        for rule in rules
        if (api_group in rule["apiGroups"] or "*" in rule["apiGroups"])
        and (resource in rule["resources"] or "*" in rule["resources"])
        for verb in rule["verbs"]
    }


def parse_documents(value: str) -> list[dict[str, Any]]:
    """Parse a multi-document YAML string into objects."""
    return [item for item in yaml.safe_load_all(value) if isinstance(item, dict)]


def write_values(directory: pathlib.Path, name: str, values: dict[str, Any]) -> pathlib.Path:
    """Write values to a YAML file and return its path."""
    path = directory / name
    path.write_text(yaml.safe_dump(values, sort_keys=False), encoding="utf-8")
    return path


def node(
    name: str,
    vtep_ip: str,
    uplinks: list[dict[str, Any]],
    asn: int | None = None,
) -> dict[str, Any]:
    """Build one evpn.nodes entry."""
    value: dict[str, Any] = {"name": name, "vtepIP": vtep_ip, "uplinks": uplinks}
    if asn is not None:
        value["asn"] = asn
    return value


def uplink(
    interface: str,
    peer_address: str | None,
    peer_asn: int = 4200300002,
    address: str | None = None,
    unnumbered: bool = False,
    role: str | None = None,
) -> dict[str, Any]:
    """Build one evpn.nodes[].uplinks entry."""
    value: dict[str, Any] = {
        "interface": interface,
        "peerASN": peer_asn,
    }
    if peer_address is not None:
        value["peerAddress"] = peer_address
    if address is not None:
        value["address"] = address
    if unnumbered:
        value["unnumbered"] = True
    if role is not None:
        value["role"] = role
    return value


def base_values(nodes: list[dict[str, Any]]) -> dict[str, Any]:
    """Build a minimal valid EVPN values document."""
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
    """EVPN disabled renders no EVPN resources."""
    deps = render("osac-deps", DEPS_CHART)
    infra = render("osac-infra", INFRA_CHART)
    assert not any(
        item.get("metadata", {}).get("namespace") == "openshift-nmstate"
        for item in deps
    ), "disabled EVPN must not render NMState operator resources"
    absent(infra, "ConfigMap", "osac-infra-evpn-config")
    absent(infra, "Job", "osac-infra-configure-evpn")


def test_granted_verbs_aggregates_rules_and_wildcards() -> None:
    """Wildcard rules contribute grants for every resource."""
    rules = [
        {"apiGroups": ["nmstate.io"], "resources": ["nmstates"], "verbs": ["get"]},
        {"apiGroups": ["*"], "resources": ["*"], "verbs": ["watch"]},
    ]
    assert granted_verbs(rules, "nmstate.io", "nmstates") == {"get", "watch"}


def test_readiness_uses_operator_labels() -> None:
    """Readiness checks use the operator's own label selectors."""
    script = HOOK_SCRIPT.read_text(encoding="utf-8")
    assert '-l "app=frr-k8s,component=frr-k8s"' in script
    assert (
        "-l 'operators.coreos.com/kubernetes-nmstate-operator.openshift-nmstate'"
        in script
    )


def test_job_deadline_covers_bgp_wait() -> None:
    """The hook deadline covers every per-peer BGP wait."""
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
    """A failed prune listing aborts the hook before any deletion."""
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


def test_bgp_wait_requires_bfd_and_redacts_identifiers() -> None:
    """The BGP readiness wait requires BFD Up and logs no identifiers."""
    node_hostname = "worker-0.internal.example.com"
    peer_ip = "169.254.133.1"
    for bfd_status, expected_success in (("Up", True), ("Down", False)):
        with tempfile.TemporaryDirectory(prefix="evpn-bfd-wait-") as temp:
            directory = pathlib.Path(temp)
            config_dir = directory / "config"
            bin_dir = directory / "bin"
            config_dir.mkdir()
            bin_dir.mkdir()

            (config_dir / "settings.env").write_text(
                "CONFIGURE_CLUSTER_NETWORK=false\n"
                "WAIT_FOR_ESTABLISHED=true\n"
                "WAIT_FOR_BFD=true\n"
                "BGP_TIMEOUT_SECONDS=0\n",
                encoding="utf-8",
            )
            (config_dir / "nodes.txt").write_text(
                node_hostname + "\n", encoding="utf-8"
            )
            (config_dir / "peers.txt").write_text(
                f"{node_hostname} {peer_ip}\n", encoding="utf-8"
            )
            for filename in (
                "cluster-network-patch.json",
                "nmstate.yaml",
                "nncp.yaml",
                "vtep.yaml",
                "frr.yaml",
                "routeadvertisements.yaml",
            ):
                (config_dir / filename).write_text("", encoding="utf-8")

            mock_oc = bin_dir / "oc"
            mock_oc.write_text(
                "#!/usr/bin/env bash\n"
                "set -euo pipefail\n"
                "case \"${1:-} ${2:-}\" in\n"
                "  'get network.operator.openshift.io') echo OVNKubernetes ;;\n"
                "  'get pods') echo True ;;\n"
                "  'get csv') echo Succeeded ;;\n"
                "  'get vtep.k8s.ovn.org')\n"
                "    if [[ \"${5:-}\" == *reason* ]]; then echo Allocated; "
                "else echo True; fi\n"
                "    ;;\n"
                "  'get bgpsessionstates.frrk8s.metallb.io')\n"
                f"    echo 'Established {bfd_status}' ;;\n"
                "esac\n",
                encoding="utf-8",
            )
            mock_oc.chmod(0o755)

            env = os.environ.copy()
            env["CONFIG_DIR"] = str(config_dir)
            env["PATH"] = f"{bin_dir}{os.pathsep}{env['PATH']}"
            result = subprocess.run(
                ["bash", str(HOOK_SCRIPT)],
                capture_output=True,
                text=True,
                env=env,
                check=False,
                timeout=30,
            )

            output = result.stdout + result.stderr
            assert (result.returncode == 0) is expected_success, result.stderr
            assert node_hostname not in output, (
                "the hook logged the node hostname"
            )
            assert peer_ip not in output, "the hook logged the peer address"
            if not expected_success:
                assert (
                    "Timed out waiting for BGP session for peers.txt entry 1"
                    in result.stderr
                )
                assert "Established Down" in output


def test_nncp_failure_redacts_node_name() -> None:
    """An NNCP readiness failure names the entry, not the node."""
    node_hostname = "worker-0.internal.example.com"
    with tempfile.TemporaryDirectory(prefix="evpn-nncp-wait-") as temp:
        directory = pathlib.Path(temp)
        config_dir = directory / "config"
        bin_dir = directory / "bin"
        config_dir.mkdir()
        bin_dir.mkdir()

        (config_dir / "settings.env").write_text(
            "CONFIGURE_CLUSTER_NETWORK=false\n"
            "WAIT_FOR_ESTABLISHED=false\n"
            "BGP_TIMEOUT_SECONDS=0\n",
            encoding="utf-8",
        )
        (config_dir / "nodes.txt").write_text(
            node_hostname + "\n", encoding="utf-8"
        )
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

        mock_oc = bin_dir / "oc"
        mock_oc.write_text(
            "#!/usr/bin/env bash\n"
            "set -euo pipefail\n"
            "case \"${1:-} ${2:-}\" in\n"
            "  'get network.operator.openshift.io') echo OVNKubernetes ;;\n"
            "  'get pods') echo True ;;\n"
            "  'get csv') echo Succeeded ;;\n"
            "  'get vtep.k8s.ovn.org')\n"
            "    if [[ \"${5:-}\" == *reason* ]]; then echo Allocated; "
            "else echo True; fi\n"
            "    ;;\n"
            "  'wait nncp/osac-evpn-'*)\n"
            f"    echo 'timed out waiting for {node_hostname}' >&2; exit 1 ;;\n"
            "  'get nncp/osac-evpn-'*) echo 'Available=False/Error' ;;\n"
            "esac\n",
            encoding="utf-8",
        )
        mock_oc.chmod(0o755)

        env = os.environ.copy()
        env["CONFIG_DIR"] = str(config_dir)
        env["PATH"] = f"{bin_dir}{os.pathsep}{env['PATH']}"
        result = subprocess.run(
            ["bash", str(HOOK_SCRIPT)],
            capture_output=True,
            text=True,
            env=env,
            check=False,
            timeout=30,
        )

        output = result.stdout + result.stderr
        assert result.returncode != 0, "the hook must fail when NNCP is not ready"
        assert "nodes.txt entry 1" in result.stderr
        assert node_hostname not in output, "the hook logged the node hostname"
        assert "Available=False" in output


def test_single_node() -> None:
    """A single numbered peer renders the full EVPN setup."""
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
    assert "ebgpMultiHop" not in router["neighbors"][0], (
        "numbered underlay peers are directly connected; multihop would force "
        "BFD into multihop mode"
    )
    assert "bfdProfiles" not in underlay["spec"]["bgp"]
    assert "bfdProfile" not in router["neighbors"][0]
    assert "WAIT_FOR_BFD=false" in data["settings.env"]
    assert "e+09" not in str(infra), "large ASNs must not render as float notation"
    assert data["peers.txt"].splitlines() == ["worker-0 169.254.133.3"]


def test_multiple_nodes() -> None:
    """Multiple nodes render one NNCP and underlay FRRConfiguration each."""
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
    assert "bgp bestpath as-path multipath-relax" not in raw_config
    assert "maximum-paths 16" not in raw_config
    assert "neighbor 169.254.134.1 activate" in raw_config
    assert "neighbor 169.254.135.1 activate" in raw_config
    assert len(data["peers.txt"].splitlines()) == 3


def test_active_active_bgp() -> None:
    """Render both uplinks with IPv4 multipath and no active-backup policy."""
    values = base_values(
        [
            node(
                "worker-0",
                "10.200.255.1",
                [uplink("ens6", "169.254.133.1"), uplink("ens7", "169.254.134.1")],
            )
        ]
    )
    values["evpn"]["bgp"]["activeActive"] = True
    with tempfile.TemporaryDirectory(prefix="evpn-values-") as temp:
        values_file = write_values(pathlib.Path(temp), "active-active.yaml", values)
        infra = render("osac-infra", INFRA_CHART, "--values", str(values_file))

    raw = named(
        parse_documents(config_data(infra)["frr.yaml"]),
        "FRRConfiguration",
        "osac-evpn-raw-worker-0",
    )
    raw_config = raw["spec"]["raw"]["rawConfig"]
    assert "bgp bestpath as-path multipath-relax" in raw_config
    ipv4_af = raw_config.split("address-family ipv4 unicast\n", 1)[1].split(
        "exit-address-family", 1
    )[0]
    assert "maximum-paths 16" in ipv4_af
    assert "address-family l2vpn evpn" in raw_config
    assert "neighbor 169.254.133.1 activate" in raw_config
    assert "neighbor 169.254.134.1 activate" in raw_config
    assert "route-map EVPN-" not in raw_config


def test_unnumbered_active_active_bgp() -> None:
    """RFC 5549 peers rely on the frr-k8s generated IPv4 unicast filters."""
    values = base_values(
        [node("worker-0", "10.200.255.1", [uplink("ens6", None, unnumbered=True)])]
    )
    values["evpn"]["bgp"]["activeActive"] = True
    with tempfile.TemporaryDirectory(prefix="evpn-unnumbered-active-active-") as temp:
        values_file = write_values(pathlib.Path(temp), "unnumbered.yaml", values)
        infra = render("osac-infra", INFRA_CHART, "--values", str(values_file))

    raw = named(
        parse_documents(config_data(infra)["frr.yaml"]),
        "FRRConfiguration",
        "osac-evpn-raw-worker-0",
    )
    raw_config = raw["spec"]["raw"]["rawConfig"]
    ipv4_af = raw_config.split("address-family ipv4 unicast\n", 1)[1].split(
        "exit-address-family", 1
    )[0]
    assert "maximum-paths 16" in ipv4_af
    assert "route-map" not in raw_config
    assert "ipv6 prefix-list" not in raw_config


def test_active_backup_bgp() -> None:
    """Active-backup renders role-based IPv4 unicast and EVPN policy."""
    values = base_values(
        [
            node(
                "worker-0",
                "10.200.255.1",
                [
                    uplink("ens5f1", "169.254.133.3", role="backup"),
                    uplink("ens5f0", "169.254.133.1", role="active"),
                ],
            )
        ]
    )
    values["evpn"]["bgp"]["activeBackup"] = True
    with tempfile.TemporaryDirectory(prefix="evpn-active-backup-") as temp:
        values_file = write_values(pathlib.Path(temp), "active-backup.yaml", values)
        infra = render("osac-infra", INFRA_CHART, "--values", str(values_file))

    raw = named(
        parse_documents(config_data(infra)["frr.yaml"]),
        "FRRConfiguration",
        "osac-evpn-raw-worker-0",
    )
    raw_config = raw["spec"]["raw"]["rawConfig"]
    assert (
        "ip prefix-list EVPN-REMOTE-VTEPS seq 1 "
        "permit 10.200.0.0/16 ge 32 le 32" in raw_config
    )
    assert "ip prefix-list EVPN-LOCAL-VTEP" not in raw_config
    assert "set local-preference 200" in raw_config
    assert "set local-preference 100" in raw_config

    # EVPN route-maps apply to every route and never match on a prefix.
    active_in = raw_config.split(
        "route-map EVPN-ACTIVE-IN permit 10\n", 1
    )[1].split("route-map EVPN-ACTIVE-OUT permit 10", 1)[0]
    backup_in = raw_config.split(
        "route-map EVPN-BACKUP-IN permit 10\n", 1
    )[1].split("route-map EVPN-BACKUP-OUT permit 10", 1)[0]
    backup_out = raw_config.split(
        "route-map EVPN-BACKUP-OUT permit 10\n", 1
    )[1].split("route-map ", 1)[0]
    assert "set local-preference 200" in active_in
    assert "set local-preference 100" in backup_in
    assert "description EVPN active uplink inbound" in active_in
    assert "description EVPN backup uplink inbound" in backup_in
    assert "description EVPN backup uplink outbound" in backup_out
    assert "set local-preference" not in backup_out
    assert "set as-path prepend 4209999999 4209999999 4209999999" in backup_out
    assert "match " not in active_in
    assert "match " not in backup_in
    assert "match " not in backup_out

    active_out = raw_config.split(
        "route-map EVPN-ACTIVE-OUT permit 10\n", 1
    )[1].split("route-map ", 1)[0]
    assert "description EVPN active uplink outbound" in active_out
    assert "match " not in active_out
    assert "set " not in active_out

    # frr-k8s always attaches its own <peer>-in/-out maps in address-family
    # ipv4 unicast and its rendered config wins over the raw block, so the
    # active/backup precedence is merged into those generated maps.
    ipv4_active_in = raw_config.split(
        "route-map 169.254.133.1-in permit 1\n", 1
    )[1].split("route-map ", 1)[0]
    ipv4_backup_in = raw_config.split(
        "route-map 169.254.133.3-in permit 1\n", 1
    )[1].split("route-map ", 1)[0]
    ipv4_backup_out = raw_config.split(
        "route-map 169.254.133.3-out permit 1\n", 1
    )[1].split("route-map ", 1)[0]
    assert "description EVPN active uplink IPv4 unicast inbound" in ipv4_active_in
    assert "match ip address prefix-list EVPN-REMOTE-VTEPS" in ipv4_active_in
    assert "set local-preference 200" in ipv4_active_in
    assert "description EVPN backup uplink IPv4 unicast inbound" in ipv4_backup_in
    assert "match ip address prefix-list EVPN-REMOTE-VTEPS" in ipv4_backup_in
    assert "set local-preference 100" in ipv4_backup_in
    assert (
        "match ip address prefix-list 169.254.133.3-allowed-ipv4"
        in ipv4_backup_out
    )
    assert (
        "set as-path prepend 4209999999 4209999999 4209999999" in ipv4_backup_out
    )
    assert "route-map 169.254.133.1-out permit 1" not in raw_config
    assert "EVPN-IPV4" not in raw_config
    assert "address-family ipv4 unicast" not in raw_config
    assert "fe80::/10" not in raw_config

    l2vpn_af = raw_config.split("address-family l2vpn evpn\n", 1)[1].split(
        "exit-address-family", 1
    )[0]

    assert (
        "neighbor 169.254.133.3 route-map "
        "EVPN-BACKUP-IN in" in l2vpn_af
    )
    assert (
        "neighbor 169.254.133.1 route-map "
        "EVPN-ACTIVE-IN in" in l2vpn_af
    )
    assert (
        "neighbor 169.254.133.3 route-map "
        "EVPN-BACKUP-OUT out" in l2vpn_af
    )
    assert (
        "neighbor 169.254.133.1 route-map "
        "EVPN-ACTIVE-OUT out" in l2vpn_af
    )
    assert "-IPV4-" not in l2vpn_af


def test_unnumbered_active_backup_bgp() -> None:
    """Unnumbered active-backup uses interface-named route-maps."""
    values = base_values(
        [
            node(
                "worker-0",
                "10.200.255.1",
                [
                    uplink("ens5f0", None, unnumbered=True, role="active"),
                    uplink("ens5f1", None, unnumbered=True, role="backup"),
                ],
            )
        ]
    )
    values["evpn"]["bgp"]["activeBackup"] = True
    with tempfile.TemporaryDirectory(prefix="evpn-unnumbered-active-backup-") as temp:
        values_file = write_values(pathlib.Path(temp), "unnumbered.yaml", values)
        infra = render("osac-infra", INFRA_CHART, "--values", str(values_file))

    raw = named(
        parse_documents(config_data(infra)["frr.yaml"]),
        "FRRConfiguration",
        "osac-evpn-raw-worker-0",
    )
    raw_config = raw["spec"]["raw"]["rawConfig"]
    assert "ipv6 prefix-list" not in raw_config
    assert "fe80::/10" not in raw_config
    active_in = raw_config.split(
        "route-map ens5f0-in permit 1\n", 1
    )[1].split("route-map ", 1)[0]
    backup_in = raw_config.split(
        "route-map ens5f1-in permit 1\n", 1
    )[1].split("route-map ", 1)[0]
    backup_out = raw_config.split(
        "route-map ens5f1-out permit 1\n", 1
    )[1].split("route-map ", 1)[0]
    assert "match ip address prefix-list EVPN-REMOTE-VTEPS" in active_in
    assert "set local-preference 200" in active_in
    assert "match ip address prefix-list EVPN-REMOTE-VTEPS" in backup_in
    assert "set local-preference 100" in backup_in
    assert "match ip address prefix-list ens5f1-allowed-ipv4" in backup_out
    assert "set as-path prepend 4209999999 4209999999 4209999999" in backup_out
    assert "route-map ens5f0-out permit 1" not in raw_config
    assert "EVPN-IPV4" not in raw_config
    assert "address-family ipv4 unicast" not in raw_config
    assert "neighbor ens5f0 route-map EVPN-ACTIVE-IN in" in raw_config
    assert "neighbor ens5f0 route-map EVPN-ACTIVE-OUT out" in raw_config
    assert "neighbor ens5f1 route-map EVPN-BACKUP-IN in" in raw_config
    assert "neighbor ens5f1 route-map EVPN-BACKUP-OUT out" in raw_config


def test_active_backup_requires_l2vpn_enabled() -> None:
    """Active-backup requires EVPN to be enabled."""
    values = base_values(
        [node("worker-0", "10.200.255.1", [uplink("ens5f0", "169.254.133.1")])]
    )
    values["evpn"]["enabled"] = False
    values["evpn"]["bgp"]["activeBackup"] = True
    with tempfile.TemporaryDirectory(prefix="evpn-active-backup-disabled-") as temp:
        values_file = write_values(pathlib.Path(temp), "disabled.yaml", values)
        infra = render("osac-infra", INFRA_CHART, "--values", str(values_file))

    absent(infra, "ConfigMap", "osac-infra-evpn-config")
    absent(infra, "Job", "osac-infra-configure-evpn")


def test_active_backup_requires_two_uplinks() -> None:
    """Active-backup needs at least two uplinks per node."""
    values = base_values(
        [node("worker-0", "10.200.255.1", [uplink("ens5f0", "169.254.133.1")])]
    )
    values["evpn"]["bgp"]["activeBackup"] = True
    with tempfile.TemporaryDirectory(prefix="evpn-active-backup-single-uplink-") as temp:
        values_file = write_values(pathlib.Path(temp), "single-uplink.yaml", values)
        expect_render_failure(INFRA_CHART, values_file)


def test_active_backup_requires_explicit_uplink_roles() -> None:
    """Every uplink needs an explicit role in active-backup mode."""
    values = base_values(
        [
            node(
                "worker-0",
                "10.200.255.1",
                [
                    uplink("ens5f0", "169.254.133.1"),
                    uplink("ens5f1", "169.254.133.3", role="backup"),
                ],
            )
        ]
    )
    values["evpn"]["bgp"]["activeBackup"] = True
    with tempfile.TemporaryDirectory(prefix="evpn-active-backup-missing-role-") as temp:
        values_file = write_values(pathlib.Path(temp), "missing-role.yaml", values)
        expect_render_failure(INFRA_CHART, values_file)


def test_active_backup_requires_one_active_and_one_backup() -> None:
    """Active-backup needs exactly one active and one backup uplink."""
    for roles in (["backup", "backup"], ["active", "active"]):
        values = base_values(
            [
                node(
                    "worker-0",
                    "10.200.255.1",
                    [
                        uplink("ens5f0", "169.254.133.1", role=roles[0]),
                        uplink("ens5f1", "169.254.133.3", role=roles[1]),
                    ],
                )
            ]
        )
        values["evpn"]["bgp"]["activeBackup"] = True
        with tempfile.TemporaryDirectory(prefix="evpn-active-backup-role-count-") as temp:
            values_file = write_values(pathlib.Path(temp), "invalid-roles.yaml", values)
            expect_render_failure(INFRA_CHART, values_file)


def test_active_backup_is_mutually_exclusive_with_active_active() -> None:
    """Reject both modes even when the active-backup uplinks are valid."""
    values = base_values(
        [
            node(
                "worker-0",
                "10.200.255.1",
                [
                    uplink("ens5f0", "169.254.133.1", role="active"),
                    uplink("ens5f1", "169.254.133.3", role="backup"),
                ],
            )
        ]
    )
    values["evpn"]["bgp"]["activeActive"] = True
    values["evpn"]["bgp"]["activeBackup"] = True
    with tempfile.TemporaryDirectory(prefix="evpn-bgp-modes-") as temp:
        values_file = write_values(pathlib.Path(temp), "conflicting-modes.yaml", values)
        expect_render_failure(INFRA_CHART, values_file)


def test_uplink_without_address() -> None:
    """An uplink without a CIDR leaves the interface unconfigured."""
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


def test_unnumbered_uplink() -> None:
    """An unnumbered uplink renders an interface-based neighbor."""
    values = base_values(
        [node("worker-0", "10.200.255.1", [uplink("ens6", None, unnumbered=True)])]
    )
    with tempfile.TemporaryDirectory(prefix="evpn-unnumbered-") as temp:
        values_file = write_values(pathlib.Path(temp), "unnumbered.yaml", values)
        infra = render("osac-infra", INFRA_CHART, "--values", str(values_file))

    data = config_data(infra)
    assert data["peers.txt"].splitlines() == ["worker-0 ens6"]
    nncp = named(parse_documents(data["nncp.yaml"]), "NodeNetworkConfigurationPolicy", "osac-evpn-worker-0")
    ethernet = next(item for item in nncp["spec"]["desiredState"]["interfaces"] if item["name"] == "ens6")
    assert ethernet["ipv4"] == {"enabled": False}
    assert ethernet["ipv6"] == {"enabled": True, "dhcp": False, "autoconf": False}

    underlay = named(parse_documents(data["frr.yaml"]), "FRRConfiguration", "osac-evpn-worker-0")
    neighbor = underlay["spec"]["bgp"]["routers"][0]["neighbors"][0]
    assert neighbor["interface"] == "ens6"
    assert "address" not in neighbor
    assert "ebgpMultiHop" not in neighbor
    assert "sourceaddress" not in neighbor
    raw = named(parse_documents(data["frr.yaml"]), "FRRConfiguration", "osac-evpn-raw-worker-0")
    raw_config = raw["spec"]["raw"]["rawConfig"]
    assert "neighbor ens6 activate" in raw_config
    assert "ipv6 prefix-list" not in raw_config
    assert "route-map" not in raw_config


def test_bfd_profile_is_single_hop() -> None:
    """BFD attaches to directly connected peers without multihop."""
    values = base_values(
        [
            node(
                "worker-0",
                "10.200.255.1",
                [
                    uplink("ens6", "169.254.133.1", address="169.254.133.2/31"),
                    uplink("ens7", None, peer_asn=4200300003, unnumbered=True),
                ],
            )
        ]
    )
    values["evpn"]["bgp"].update(
        {
            "bfdProfile": "evpn-underlay",
            "bfdReceiveInterval": 150,
            "bfdTransmitInterval": 200,
            "bfdDetectMultiplier": 4,
        }
    )
    with tempfile.TemporaryDirectory(prefix="evpn-bfd-") as temp:
        values_file = write_values(pathlib.Path(temp), "bfd.yaml", values)
        infra = render("osac-infra", INFRA_CHART, "--values", str(values_file))

    data = config_data(infra)
    assert "WAIT_FOR_BFD=true" in data["settings.env"]
    underlay = named(
        parse_documents(data["frr.yaml"]), "FRRConfiguration", "osac-evpn-worker-0"
    )
    bgp = underlay["spec"]["bgp"]
    assert bgp["bfdProfiles"] == [
        {
            "name": "evpn-underlay",
            "receiveInterval": 150,
            "transmitInterval": 200,
            "detectMultiplier": 4,
        }
    ]
    numbered, unnumbered = bgp["routers"][0]["neighbors"]
    assert numbered["address"] == "169.254.133.1"
    assert numbered["bfdProfile"] == "evpn-underlay"
    assert "ebgpMultiHop" not in numbered
    assert unnumbered["interface"] == "ens7"
    assert unnumbered["bfdProfile"] == "evpn-underlay"
    assert "ebgpMultiHop" not in unnumbered


def test_uplinks_reject_duplicate_interface() -> None:
    """A physical interface may not serve two uplinks."""
    # Interface identity is independent of peer identity. A numbered and an
    # unnumbered uplink, or two numbered uplinks, must not share a physical
    # interface: NMState keeps the last entry for a name, so the earlier
    # interface entry (and the addressing its BGP peer needs) is dropped.
    duplicate_interface = [
        (
            "numbered-then-unnumbered.yaml",
            base_values(
                [
                    node(
                        "worker-0",
                        "10.200.255.1",
                        [
                            uplink("ens6", "169.254.133.1"),
                            uplink("ens6", None, peer_asn=4200300003, unnumbered=True),
                        ],
                    )
                ]
            ),
        ),
        (
            "unnumbered-then-numbered.yaml",
            base_values(
                [
                    node(
                        "worker-0",
                        "10.200.255.1",
                        [
                            uplink("ens6", None, peer_asn=4200300003, unnumbered=True),
                            uplink("ens6", "169.254.133.1"),
                        ],
                    )
                ]
            ),
        ),
        (
            "two-numbered.yaml",
            base_values(
                [
                    node(
                        "worker-0",
                        "10.200.255.1",
                        [
                            uplink("ens6", "169.254.133.1"),
                            uplink("ens6", "169.254.134.1", peer_asn=4200300003),
                        ],
                    )
                ]
            ),
        ),
    ]
    with tempfile.TemporaryDirectory(prefix="evpn-duplicate-interface-") as temp:
        directory = pathlib.Path(temp)
        for name, values in duplicate_interface:
            values_file = write_values(directory, name, values)
            result = render_failure_result(INFRA_CHART, values_file)
            assert result.returncode != 0, (
                f"{name}: duplicate interface unexpectedly rendered"
            )
            assert "duplicate interface in uplink index" in result.stderr, (
                f"{name}: the failure did not identify the duplicate uplink"
            )


def test_nmstate_install_disabled() -> None:
    """A preinstalled NMState operator skips the subscription."""
    values = base_values([node("worker-0", "10.200.255.1", [uplink("ens6", "169.254.133.1")])])
    values["evpn"]["nmstate"]["install"] = False
    with tempfile.TemporaryDirectory(prefix="evpn-values-") as temp:
        values_file = write_values(pathlib.Path(temp), "no-subscription.yaml", values)
        deps = render("osac-deps", DEPS_CHART, "--values", str(values_file))
        infra = render("osac-infra", INFRA_CHART, "--values", str(values_file))

    absent(deps, "Subscription", "kubernetes-nmstate-operator")
    named(infra, "Job", "osac-infra-configure-evpn")


def test_invalid_values() -> None:
    """Schema and template validation reject invalid values."""
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

    invalid_bfd = base_values([valid_node])
    invalid_bfd["evpn"]["bgp"]["bfdProfile"] = "evpn-underlay"
    invalid_bfd["evpn"]["bgp"]["bfdReceiveInterval"] = 5
    invalid.append(invalid_bfd)

    with tempfile.TemporaryDirectory(prefix="evpn-invalid-") as temp:
        directory = pathlib.Path(temp)
        for index, values in enumerate(invalid):
            values_file = write_values(directory, f"invalid-{index}.yaml", values)
            expect_render_failure(INFRA_CHART, values_file)


def test_validation_errors_do_not_log_node_names() -> None:
    """Validation errors identify entries by index, never by node name."""
    # Validation errors must not echo node names or peer addresses: they can
    # be internal hostnames or router IPs, and a failed render's error text
    # ends up in installer logs. peerAddress must be IPv4 to pass the chart
    # schema, so the render reaches the template's own duplicate checks.
    node_hostname = "worker-0.internal.example.com"
    second_hostname = "worker-1.internal.example.com"
    peer_ip = "10.11.12.1"
    second_peer_ip = "10.11.12.2"
    vtep_ip = "10.200.255.1"
    duplicate_name = base_values(
        [
            node(node_hostname, "10.200.255.1", [uplink("ens6", peer_ip)]),
            node(node_hostname, "10.200.255.2", [uplink("ens7", second_peer_ip)]),
        ]
    )
    duplicate_peer = base_values(
        [
            node(
                node_hostname,
                "10.200.255.1",
                [uplink("ens6", peer_ip), uplink("ens7", peer_ip)],
            )
        ]
    )
    duplicate_vtep = base_values(
        [
            node(node_hostname, vtep_ip, [uplink("ens6", peer_ip)]),
            node(second_hostname, vtep_ip, [uplink("ens7", second_peer_ip)]),
        ]
    )
    missing_role = base_values(
        [
            node(
                node_hostname,
                "10.200.255.1",
                [
                    uplink("ens5f0", peer_ip),
                    uplink("ens5f1", second_peer_ip, role="backup"),
                ],
            )
        ]
    )
    missing_role["evpn"]["bgp"]["activeBackup"] = True
    two_active = base_values(
        [
            node(
                node_hostname,
                "10.200.255.1",
                [
                    uplink("ens5f0", peer_ip, role="active"),
                    uplink("ens5f1", second_peer_ip, role="active"),
                ],
            )
        ]
    )
    two_active["evpn"]["bgp"]["activeBackup"] = True
    with tempfile.TemporaryDirectory(prefix="evpn-nolog-") as temp:
        directory = pathlib.Path(temp)
        for name, values, expected in (
            ("duplicate-name.yaml", duplicate_name, "duplicate node name"),
            ("duplicate-peer.yaml", duplicate_peer, "duplicate peerAddress"),
            ("duplicate-vtep.yaml", duplicate_vtep, "duplicate vtepIP"),
            ("missing-role.yaml", missing_role, "must set role"),
            ("two-active.yaml", two_active, "exactly one active uplink"),
        ):
            values_file = write_values(directory, name, values)
            result = render_failure_result(INFRA_CHART, values_file)
            assert result.returncode != 0, (
                f"{name}: invalid EVPN values unexpectedly rendered"
            )
            for identifier in (
                node_hostname,
                second_hostname,
                peer_ip,
                second_peer_ip,
                vtep_ip,
                "ens5f0",
                "ens5f1",
            ):
                assert identifier not in result.stderr, (
                    f"{name}: the failure logged {identifier}"
                )
            assert expected in result.stderr, f"{name}: no {expected} message"


def main() -> None:
    """Run every EVPN render contract test."""
    test_defaults()
    test_granted_verbs_aggregates_rules_and_wildcards()
    test_readiness_uses_operator_labels()
    test_job_deadline_covers_bgp_wait()
    test_prune_listing_failure_is_fatal()
    test_bgp_wait_requires_bfd_and_redacts_identifiers()
    test_nncp_failure_redacts_node_name()
    test_single_node()
    test_multiple_nodes()
    test_active_active_bgp()
    test_unnumbered_active_active_bgp()
    test_active_backup_bgp()
    test_unnumbered_active_backup_bgp()
    test_active_backup_requires_l2vpn_enabled()
    test_active_backup_requires_two_uplinks()
    test_active_backup_requires_explicit_uplink_roles()
    test_active_backup_requires_one_active_and_one_backup()
    test_active_backup_is_mutually_exclusive_with_active_active()
    test_uplink_without_address()
    test_unnumbered_uplink()
    test_bfd_profile_is_single_hop()
    test_uplinks_reject_duplicate_interface()
    test_nmstate_install_disabled()
    test_invalid_values()
    test_validation_errors_do_not_log_node_names()
    print("EVPN host networking Helm render contract passed.")


if __name__ == "__main__":
    main()
