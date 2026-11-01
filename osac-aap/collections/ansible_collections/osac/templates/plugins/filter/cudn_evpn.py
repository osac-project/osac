"""Pure logic of the cudn_evpn Kubernetes Manager (network manager contract).

The role's task files only call the Kubernetes API. Everything they decide is
computed here from ``osac_job_vars`` and the objects they read, so it can be
tested without a cluster:

- ``cudn_evpn_plan``: checks the task input and returns the names, labels and
  manifests the task needs, or a final error for an input OSAC never sends.
- ``cudn_evpn_vtep_state`` / ``cudn_evpn_cudn_ready``: hub prerequisite and
  hub network readiness.
- ``cudn_evpn_cudn_assess``: who owns an existing hub network and whether it
  still matches this Subnet.
- ``cudn_evpn_rules_patch`` / ``cudn_evpn_membership_patch``: conditional
  JSON patches for a SecurityGroup's NetworkPolicy.

Every helper returns data and never raises for bad input, so a task can tell
a final error (``retryable: false``) from a transient one.
"""

from __future__ import annotations

import ipaddress
import re
from typing import Any

MANAGER = "cudn_evpn"
VTEP_NAME = "tenant-vtep"
MTU = 1500
MAX_RESERVED_SUBNETS = 25
MAX_INFRASTRUCTURE_SUBNETS = 4
MAX_VNI = 16777215

LABEL_TENANT = "osac.openshift.io/tenant"
LABEL_VN = "osac.openshift.io/virtualnetwork-id"
LABEL_OWNER_KIND = "osac.openshift.io/owner-kind"
LABEL_OWNER_ID = "osac.openshift.io/owner-id"
LABEL_MANAGER = "osac.openshift.io/k8s-manager"
LABEL_PRIMARY_UDN = "k8s.ovn.org/primary-user-defined-network"
LABEL_EVPN = "evpn"
LABEL_VM = "osac.openshift.io/compute-instance-id"
ANNOTATION_TENANT = "osac.openshift.io/tenant"
ANNOTATION_OWNER_REFERENCE = "osac.openshift.io/owner-reference"
ANNOTATION_OWNER_SUBNET = "osac.openshift.io/owner-subnet"

BASELINE_POLICY = "osac-baseline-deny"
NAMESPACE_PREFIX = "osac-vn-"
POLICY_PREFIX = "osac-sg-"

# Conditions OVN-Kubernetes 4.22 sets on an EVPN ClusterUserDefinedNetwork. It
# reports no Ready condition and no observedGeneration.
CUDN_CONDITIONS = ("TransportAccepted", "NetworkCreated", "NetworkAllocationSucceeded")

_UUID = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")
_LABEL_VALUE = re.compile(r"^([A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?)?$")

TASKS = (
    "create_virtual_network",
    "delete_virtual_network",
    "create_subnet",
    "delete_subnet",
    "create_security_group",
    "delete_security_group",
    "apply_network_attachment",
    "delete_network_attachment",
)
_KIND_OF_TASK = {
    "create_virtual_network": "VirtualNetwork",
    "delete_virtual_network": "VirtualNetwork",
    "create_subnet": "Subnet",
    "delete_subnet": "Subnet",
    "create_security_group": "SecurityGroup",
    "delete_security_group": "SecurityGroup",
    "apply_network_attachment": "NetworkAttachment",
    "delete_network_attachment": "NetworkAttachment",
}


class _InputError(Exception):
    """An input OSAC never sends: the task fails as final."""


class _LimitError(Exception):
    """An implementation limit of cudn_evpn: the task fails as final."""


# ---------------------------------------------------------------------------
# Input checks
# ---------------------------------------------------------------------------


def _mapping(value: Any, name: str) -> dict:
    if not isinstance(value, dict):
        raise _InputError(f"{name} must be a mapping")
    return value


def _api_id(value: Any, name: str) -> str:
    if not isinstance(value, str) or not _UUID.match(value):
        raise _InputError(f"{name} must be a lowercase UUID API ID")
    return value


def _label_value(value: Any, name: str) -> str:
    if not isinstance(value, str) or not value or not _LABEL_VALUE.match(value) or len(value) > 63:
        raise _InputError(f"{name} must be a non-empty Kubernetes label value")
    return value


def _snapshot(job: dict, key: str, kind: str) -> dict:
    snap = _mapping(job.get(key), key)
    if snap.get("kind") != kind:
        raise _InputError(f"{key}.kind must be {kind}")
    _api_id(snap.get("id"), f"{key}.id")
    return snap


def _cidr(value: Any, name: str) -> ipaddress.IPv4Network:
    """A canonical IPv4 CIDR (zero host bits, prefix written out)."""
    try:
        if not isinstance(value, str) or "/" not in value:
            raise ValueError
        net = ipaddress.ip_network(value, strict=True)
    except ValueError as exc:
        raise _InputError(f"{name} must be a canonical IPv4 CIDR") from exc
    if net.version != 4 or str(net) != value:
        raise _InputError(f"{name} must be a canonical IPv4 CIDR")
    return net


def _address(value: Any, name: str) -> ipaddress.IPv4Address:
    try:
        if not isinstance(value, str):
            raise ValueError
        addr = ipaddress.ip_address(value)
    except ValueError as exc:
        raise _InputError(f"{name} must be a canonical IPv4 address") from exc
    if addr.version != 4 or str(addr) != value:
        raise _InputError(f"{name} must be a canonical IPv4 address")
    return addr


def _int(value: Any, name: str, low: int, high: int) -> int:
    # bool is an int in Python; OSAC sends JSON integers only.
    if isinstance(value, bool) or not isinstance(value, int) or not low <= value <= high:
        raise _InputError(f"{name} must be an integer from {low} to {high}")
    return value


def _owner_reference(snapshot: dict, kind: str) -> str:
    name = (snapshot.get("metadata") or {}).get("name") or snapshot["id"]
    return f"{kind}/{name}"


def segment_vnis(job: dict) -> tuple[int, int]:
    """Read the VNIs of a Subnet task (contract §4.9, EP #427).

    This is the only place that reads VNIs: the Subnet's l2_vni from the
    launch key network_segment {type: evpn, l2_vni}, and the VirtualNetwork's
    l3_vni from the stored parent snapshot, virtual_network.status.l3Vni.
    """
    segment = _mapping(job.get("network_segment"), "network_segment")
    if set(segment) != {"type", "l2_vni"}:
        raise _InputError("network_segment must have exactly the keys type and l2_vni")
    if segment["type"] != "evpn":
        raise _InputError("network_segment.type must be evpn")
    l2 = _int(segment["l2_vni"], "network_segment.l2_vni", 1, MAX_VNI)
    status = _mapping(_mapping(job.get("virtual_network"), "virtual_network").get("status"),
                      "virtual_network.status")
    l3 = _int(status.get("l3Vni"), "virtual_network.status.l3Vni", 1, MAX_VNI)
    if l2 == l3:
        raise _InputError("network_segment.l2_vni and virtual_network.status.l3Vni must differ")
    return l2, l3


def _ipam(job: dict, subnet: ipaddress.IPv4Network) -> dict:
    ipam = _mapping(job.get("ipam"), "ipam")
    gateway = _address(ipam.get("gateway_ipv4"), "ipam.gateway_ipv4")
    if gateway not in subnet:
        raise _InputError("ipam.gateway_ipv4 must be inside the Subnet CIDR")
    lists = {}
    for key in ("overlay_ipv4_cidrs", "physical_ipv4_cidrs", "lb_vip_ipv4_cidrs"):
        values = ipam.get(key)
        if not isinstance(values, list):
            raise _InputError(f"ipam.{key} must be a list")
        nets = [_cidr(v, f"ipam.{key}[{i}]") for i, v in enumerate(values)]
        for net in nets:
            if not net.subnet_of(subnet):
                raise _InputError(f"ipam.{key} must be inside the Subnet CIDR")
        lists[key] = nets
    if not lists["overlay_ipv4_cidrs"]:
        raise _InputError("ipam.overlay_ipv4_cidrs is empty: the Subnet has no VM address range")
    return {"gateway": gateway, **lists}


# ---------------------------------------------------------------------------
# Addressing
# ---------------------------------------------------------------------------


def _subtract(base: list[ipaddress.IPv4Network], remove: list[ipaddress.IPv4Network]) -> list:
    """base minus remove, as the fewest aligned CIDRs in ascending order."""
    remaining = list(base)
    for cut in remove:
        out = []
        for net in remaining:
            if net.subnet_of(cut):
                continue
            if cut.subnet_of(net):
                out.extend(net.address_exclude(cut))
            else:
                out.append(net)
        remaining = out
    return list(ipaddress.collapse_addresses(remaining))


def layer2_addressing(subnet_cidr: str, ipam: dict) -> dict:
    """The CUDN Layer2 addressing of a Subnet from its recorded layout.

    VMs get addresses only from the overlay ranges. OVN-Kubernetes serves the
    gateway (defaultGatewayIPs) and uses network + 2 as its management-port
    address; both are infrastructure addresses (EP #427), so OVN excludes them
    from VM allocation. The layout places the overlay range first, so both lie
    inside it. reservedSubnets is every other part of the Subnet: the physical
    and LB VIP ranges and anything the layout leaves out; the CRD forbids an
    overlap with infrastructureSubnets.
    """
    subnet = ipaddress.ip_network(subnet_cidr)
    gateway = ipaddress.ip_network(f"{ipam['gateway_ipv4']}/32")
    management = ipaddress.ip_network(f"{subnet.network_address + 2}/32")
    overlay = [ipaddress.ip_network(c) for c in ipam["overlay_ipv4_cidrs"]]
    infrastructure = [gateway, management]
    reserved = _subtract([subnet], overlay + infrastructure)
    if len(reserved) > MAX_RESERVED_SUBNETS:
        raise _LimitError(
            f"cudn_evpn implementation limit: the Subnet's address layout needs "
            f"{len(reserved)} reservedSubnets entries outside its VM range; "
            f"OVN-Kubernetes allows at most {MAX_RESERVED_SUBNETS}"
        )
    return {
        "subnets": [str(subnet)],
        "defaultGatewayIPs": [str(gateway.network_address)],
        "infrastructureSubnets": [str(n) for n in infrastructure],
        "reservedSubnets": [str(n) for n in reserved],
    }


# ---------------------------------------------------------------------------
# SecurityGroup rules
# ---------------------------------------------------------------------------


def _rule(rule: Any, direction: str, index: int) -> dict:
    name = f"resource.spec.{direction}[{index}]"
    rule = _mapping(rule, name)
    protocol = rule.get("protocol")
    if protocol not in ("tcp", "udp", "icmp", "all"):
        raise _InputError(f"{name}.protocol must be tcp, udp, icmp or all")
    peer_key, peer_field = ("sourceCidr", "from") if direction == "ingressRules" else ("destinationCidr", "to")
    allowed = {"protocol", peer_key}
    if protocol in ("tcp", "udp"):
        allowed |= {"portFrom", "portTo"}
    extra = set(rule) - allowed
    if extra:
        raise _InputError(f"{name} has unexpected keys {', '.join(sorted(extra))}")
    peer = _cidr(rule.get(peer_key), f"{name}.{peer_key}")
    out: dict = {peer_field: [{"ipBlock": {"cidr": str(peer)}}]}
    if protocol in ("tcp", "udp"):
        low = _int(rule.get("portFrom"), f"{name}.portFrom", 1, 65535)
        high = _int(rule.get("portTo"), f"{name}.portTo", 1, 65535)
        if low > high:
            raise _InputError(f"{name}.portFrom must not exceed portTo")
        port: dict = {"protocol": protocol.upper(), "port": low}
        if high != low:
            port["endPort"] = high
        out["ports"] = [port]
    return out


def policy_rules(spec: Any) -> dict:
    """NetworkPolicy ingress/egress for a SecurityGroup's rules.

    tcp/udp keep their destination port range, all allows every protocol to
    or from the CIDR, a direction without rules allows nothing. NetworkPolicy
    cannot match ICMP, so an icmp rule is an implementation limit.
    """
    spec = _mapping(spec, "resource.spec")
    result = {}
    for direction, field in (("ingressRules", "ingress"), ("egressRules", "egress")):
        # SecurityGroupSpec lists are omitempty: an empty direction is absent.
        rules = spec.get(direction, [])
        if not isinstance(rules, list):
            raise _InputError(f"resource.spec.{direction} must be a list")
        for i, rule in enumerate(rules):
            if isinstance(rule, dict) and rule.get("protocol") == "icmp":
                raise _LimitError(
                    "cudn_evpn implementation limit: SecurityGroup rules with protocol icmp "
                    "are not supported, because Kubernetes NetworkPolicy cannot match ICMP. "
                    f"Remove the icmp rule ({direction}[{i}]) or use protocol all for its CIDR."
                )
        result[field] = [_rule(rule, direction, i) for i, rule in enumerate(rules)]
    return result


def pod_selector(vm_ids: list) -> dict:
    """The podSelector of a group's NetworkPolicy for its bound VMs.

    Bound VMs are matched by the VM pod label. With none bound it is a
    contradictory selector that matches no pod; an empty selector would match
    every pod and "In" needs at least one value.
    """
    ids = sorted(set(vm_ids))
    if not ids:
        return {
            "matchExpressions": [
                {"key": LABEL_VM, "operator": "Exists"},
                {"key": LABEL_VM, "operator": "DoesNotExist"},
            ]
        }
    return {"matchExpressions": [{"key": LABEL_VM, "operator": "In", "values": ids}]}


def bound_ids(policy: dict) -> list:
    """The VM API IDs a group's NetworkPolicy selects ([] for the unbound selector)."""
    selector = ((policy or {}).get("spec") or {}).get("podSelector") or {}
    ids: list = []
    for expr in selector.get("matchExpressions") or []:
        if expr.get("key") == LABEL_VM and expr.get("operator") == "In":
            ids.extend(expr.get("values") or [])
    return sorted(set(ids))


# ---------------------------------------------------------------------------
# Manifests
# ---------------------------------------------------------------------------


def _meta(name: str, labels: dict, annotations: dict, namespace: str | None = None) -> dict:
    meta: dict = {"name": name, "labels": labels, "annotations": annotations}
    if namespace:
        meta["namespace"] = namespace
    return meta


def _identity(tenant: str, vn_id: str, owner_kind: str, owner_id: str) -> dict:
    return {
        LABEL_TENANT: tenant,
        LABEL_VN: vn_id,
        LABEL_OWNER_KIND: owner_kind,
        LABEL_OWNER_ID: owner_id,
        LABEL_MANAGER: MANAGER,
    }


def _namespace_manifest(tenant: str, vn: dict) -> dict:
    labels = {LABEL_PRIMARY_UDN: "", **_identity(tenant, vn["id"], "virtualnetwork", vn["id"])}
    annotations = {
        ANNOTATION_TENANT: tenant,
        ANNOTATION_OWNER_REFERENCE: _owner_reference(vn, "VirtualNetwork"),
    }
    return {
        "apiVersion": "v1",
        "kind": "Namespace",
        "metadata": _meta(NAMESPACE_PREFIX + vn["id"], labels, annotations),
    }


def _baseline_manifest(tenant: str, vn: dict) -> dict:
    """Deny all tenant traffic of every pod in the namespace both ways.

    SecurityGroup policies add what they allow. OVN-Kubernetes always allows
    ARP/ND past NetworkPolicy, but not DHCP, so the baseline allows the DHCP
    exchange with OVN's DHCP responder: requests to UDP 67 and replies to UDP 68.
    """
    namespace = NAMESPACE_PREFIX + vn["id"]
    return {
        "apiVersion": "networking.k8s.io/v1",
        "kind": "NetworkPolicy",
        "metadata": _meta(
            BASELINE_POLICY,
            _identity(tenant, vn["id"], "virtualnetwork", vn["id"]),
            {ANNOTATION_TENANT: tenant, ANNOTATION_OWNER_REFERENCE: _owner_reference(vn, "VirtualNetwork")},
            namespace,
        ),
        "spec": {
            "podSelector": {},
            "policyTypes": ["Ingress", "Egress"],
            "ingress": [{"ports": [{"protocol": "UDP", "port": 68}]}],
            "egress": [{"ports": [{"protocol": "UDP", "port": 67}]}],
        },
    }


def _cudn_manifest(tenant: str, vn: dict, subnet: dict, addressing: dict, l2_vni: int, l3_vni: int) -> dict:
    labels = {LABEL_EVPN: "true", **_identity(tenant, vn["id"], "subnet", subnet["id"])}
    annotations = {
        ANNOTATION_TENANT: tenant,
        ANNOTATION_OWNER_REFERENCE: _owner_reference(subnet, "Subnet"),
        ANNOTATION_OWNER_SUBNET: subnet["id"],
    }
    layer2: dict = {
        "role": "Primary",
        "mtu": MTU,
        "subnets": addressing["subnets"],
        "defaultGatewayIPs": addressing["defaultGatewayIPs"],
        "infrastructureSubnets": addressing["infrastructureSubnets"],
        "ipam": {"mode": "Enabled", "lifecycle": "Persistent"},
    }
    if addressing["reservedSubnets"]:
        layer2["reservedSubnets"] = addressing["reservedSubnets"]
    return {
        "apiVersion": "k8s.ovn.org/v1",
        "kind": "ClusterUserDefinedNetwork",
        "metadata": _meta(NAMESPACE_PREFIX + vn["id"], labels, annotations),
        "spec": {
            "namespaceSelector": {"matchLabels": {LABEL_VN: vn["id"], LABEL_TENANT: tenant}},
            "network": {
                "topology": "Layer2",
                "transport": "EVPN",
                "layer2": layer2,
                "evpn": {
                    "vtep": VTEP_NAME,
                    "macVRF": {"vni": l2_vni, "routeTarget": f"0:{l2_vni}"},
                    "ipVRF": {"vni": l3_vni, "routeTarget": f"0:{l3_vni}"},
                },
            },
        },
    }


def _policy_manifest(tenant: str, vn: dict, sg: dict, rules: dict) -> dict:
    return {
        "apiVersion": "networking.k8s.io/v1",
        "kind": "NetworkPolicy",
        "metadata": _meta(
            POLICY_PREFIX + sg["id"],
            _identity(tenant, vn["id"], "securitygroup", sg["id"]),
            {ANNOTATION_TENANT: tenant, ANNOTATION_OWNER_REFERENCE: _owner_reference(sg, "SecurityGroup")},
            NAMESPACE_PREFIX + vn["id"],
        ),
        "spec": {
            "podSelector": pod_selector([]),
            "policyTypes": ["Ingress", "Egress"],
            "ingress": rules["ingress"],
            "egress": rules["egress"],
        },
    }


# ---------------------------------------------------------------------------
# The plan of one task
# ---------------------------------------------------------------------------


def _plan(job: dict) -> dict:
    job = _mapping(job, "osac_job_vars")
    task = job.get("task")
    if task not in TASKS:
        raise _InputError(f"task {task!r} is not a Kubernetes Manager task")
    manager = _mapping(job.get("manager"), "manager")
    if manager.get("type") != "kubernetes":
        raise _InputError("cudn_evpn is a Kubernetes Manager; manager.type must be kubernetes")
    tenant = _label_value(job.get("tenant"), "tenant")
    kind = _KIND_OF_TASK[task]
    resource = _snapshot(job, "resource", kind)
    creates = task.startswith(("create_", "apply_"))

    if kind == "VirtualNetwork":
        vn = resource
    else:
        vn = _snapshot(job, "virtual_network", "VirtualNetwork")

    plan: dict = {
        "task": task,
        "tenant": tenant,
        "resource_id": resource["id"],
        "vn_id": vn["id"],
        "namespace": NAMESPACE_PREFIX + vn["id"],
        "cudn_name": NAMESPACE_PREFIX + vn["id"],
        "baseline_name": BASELINE_POLICY,
        "vtep_name": VTEP_NAME,
        "labels": {
            "tenant": LABEL_TENANT,
            "vn": LABEL_VN,
            "owner_kind": LABEL_OWNER_KIND,
            "owner_id": LABEL_OWNER_ID,
            "vm": LABEL_VM,
        },
        "owner_subnet_annotation": ANNOTATION_OWNER_SUBNET,
    }

    if kind == "VirtualNetwork":
        plan["namespace_manifest"] = _namespace_manifest(tenant, vn)
        plan["baseline_manifest"] = _baseline_manifest(tenant, vn)

    elif kind == "Subnet":
        plan["subnet_id"] = resource["id"]
        if creates:
            cidr = _cidr((resource.get("spec") or {}).get("ipv4Cidr"), "resource.spec.ipv4Cidr")
            vn_cidr = (vn.get("spec") or {}).get("ipv4Cidr")
            if vn_cidr is not None and not cidr.subnet_of(_cidr(vn_cidr, "virtual_network.spec.ipv4Cidr")):
                raise _InputError("the Subnet CIDR must be inside the VirtualNetwork CIDR")
            ipam = _ipam(job, cidr)
            l2_vni, l3_vni = segment_vnis(job)
            addressing = layer2_addressing(
                str(cidr),
                {
                    "gateway_ipv4": str(ipam["gateway"]),
                    "overlay_ipv4_cidrs": [str(n) for n in ipam["overlay_ipv4_cidrs"]],
                },
            )
            plan["cudn_manifest"] = _cudn_manifest(tenant, vn, resource, addressing, l2_vni, l3_vni)

    elif kind == "SecurityGroup":
        plan["sg_id"] = resource["id"]
        plan["policy_name"] = POLICY_PREFIX + resource["id"]
        if creates:
            rules = policy_rules(resource.get("spec"))
            plan["rules"] = rules
            plan["policy_manifest"] = _policy_manifest(tenant, vn, resource, rules)

    else:  # NetworkAttachment
        subnet = _snapshot(job, "subnet", "Subnet")
        target = _mapping(job.get("target"), "target")
        if target.get("kind") != "ComputeInstance":
            raise _InputError("cudn_evpn attaches only VMs: target.kind must be ComputeInstance")
        target_id = _api_id(target.get("id"), "target.id")
        if target_id != resource["id"]:
            raise _InputError("target.id must equal resource.id")
        attachment = _mapping(job.get("network_attachment"), "network_attachment")
        if attachment.get("subnet") != subnet["id"]:
            raise _InputError("network_attachment.subnet must equal subnet.id")
        interface = _mapping(attachment.get("interface"), "network_attachment.interface")
        if interface.get("type") != "overlay":
            raise _InputError("cudn_evpn attaches only overlay interfaces")
        groups = attachment.get("security_group_ids")
        if not isinstance(groups, list):
            raise _InputError("network_attachment.security_group_ids must be a list")
        groups = [_api_id(g, f"network_attachment.security_group_ids[{i}]") for i, g in enumerate(groups)]
        if creates and not groups:
            raise _InputError("network_attachment.security_group_ids must not be empty")
        if len(set(groups)) != len(groups):
            raise _InputError("network_attachment.security_group_ids must not repeat an ID")
        plan.update(
            {
                "subnet_id": subnet["id"],
                "target_id": target_id,
                "security_group_ids": groups,
                "policy_names": [POLICY_PREFIX + g for g in groups],
                "result": {"vm_attachment": {"namespace": NAMESPACE_PREFIX + vn["id"]}},
            }
        )
    return plan


def cudn_evpn_plan(job: Any) -> dict:
    """The plan of one task, or {'error': {message, retryable: false}}."""
    try:
        plan = _plan(job)
    except _InputError as exc:
        return {"error": {"message": f"cudn_evpn received an invalid task input: {exc}", "retryable": False}}
    except _LimitError as exc:
        return {"error": {"message": str(exc), "retryable": False}}
    plan["error"] = None
    return plan


# ---------------------------------------------------------------------------
# Settings and prerequisites
# ---------------------------------------------------------------------------


def _condition(obj: dict, kind: str) -> dict | None:
    for cond in ((obj or {}).get("status") or {}).get("conditions") or []:
        if cond.get("type") == kind:
            return cond
    return None


def cudn_evpn_vtep_state(info: Any, name: str = VTEP_NAME) -> dict:
    """Whether the installer's VTEP exists and is Accepted (k8s_info result)."""
    info = info or {}
    if info.get("api_found") is False:
        return {"ready": False, "message": f"the VTEP API (k8s.ovn.org/v1 VTEP) is not installed on the hub, so VTEP {name} is missing"}
    resources = info.get("resources") or []
    if not resources:
        return {"ready": False, "message": f"VTEP {name} does not exist on the hub"}
    accepted = _condition(resources[0], "Accepted")
    if not accepted or accepted.get("status") != "True":
        reason = (accepted or {}).get("reason") or "no Accepted condition yet"
        return {"ready": False, "message": f"VTEP {name} is not Accepted ({reason})"}
    return {"ready": True, "message": ""}


def cudn_evpn_cudn_ready(cudn: Any) -> dict:
    """Whether a hub network reports every OVN-Kubernetes condition True."""
    pending = []
    for kind in CUDN_CONDITIONS:
        cond = _condition(cudn or {}, kind)
        if not cond or cond.get("status") != "True":
            pending.append(f"{kind}={(cond or {}).get('reason') or 'pending'}")
    return {"ready": not pending, "pending": pending}


def _subset(desired: Any, live: Any, path: str, drift: list) -> None:
    """Record each path where live differs from desired (desired keys only)."""
    if isinstance(desired, dict):
        if not isinstance(live, dict):
            drift.append(path)
            return
        for key, value in desired.items():
            _subset(value, live.get(key), f"{path}.{key}", drift)
    elif desired != live:
        drift.append(path)


def cudn_evpn_cudn_assess(cudn: Any, plan: dict) -> dict:
    """Classify an existing hub network for create_subnet and apply.

    state is one of:
      foreign     - not this VirtualNetwork's network (wrong tenant or VN labels)
      terminating - being deleted; its claim is released when it is gone
      owner       - owned by this Subnet (drift lists network fields that differ,
                    metadata_patch restores labels and annotations by merge,
                    selector_patch replaces a changed namespace selector)
      other       - owned by another Subnet of the VirtualNetwork (owner names it)
    """
    meta = (cudn or {}).get("metadata") or {}
    labels = meta.get("labels") or {}
    annotations = meta.get("annotations") or {}
    owner = annotations.get(ANNOTATION_OWNER_SUBNET, "")
    result = {"state": "", "owner": owner, "drift": [], "metadata_patch": None, "selector_patch": None}
    if labels.get(LABEL_VN) != plan["vn_id"] or labels.get(LABEL_TENANT) != plan["tenant"]:
        result["state"] = "foreign"
        return result
    if meta.get("deletionTimestamp"):
        result["state"] = "terminating"
        return result
    if owner != plan.get("subnet_id"):
        result["state"] = "other"
        return result
    result["state"] = "owner"
    desired = plan.get("cudn_manifest")
    if desired:
        drift: list = []
        _subset(desired["spec"]["network"], ((cudn or {}).get("spec") or {}).get("network"), "spec.network", drift)
        result["drift"] = drift
        wanted_labels = desired["metadata"]["labels"]
        wanted_annotations = desired["metadata"]["annotations"]
        wanted_selector = desired["spec"]["namespaceSelector"]
        patch: dict = {}
        if any(labels.get(k) != v for k, v in wanted_labels.items()):
            patch.setdefault("metadata", {})["labels"] = wanted_labels
        if any(annotations.get(k) != v for k, v in wanted_annotations.items()):
            patch.setdefault("metadata", {})["annotations"] = wanted_annotations
        result["metadata_patch"] = patch or None
        if ((cudn or {}).get("spec") or {}).get("namespaceSelector") != wanted_selector:
            result["selector_patch"] = _guard(cudn) + [
                {"op": "replace", "path": "/spec/namespaceSelector", "value": wanted_selector}
            ]
    return result


def cudn_evpn_owned(obj: Any, owner_kind: str, owner_id: str, tenant: str) -> bool:
    """Whether a hub object carries this resource's owner and tenant labels."""
    labels = ((obj or {}).get("metadata") or {}).get("labels") or {}
    return (
        labels.get(LABEL_OWNER_KIND) == owner_kind
        and labels.get(LABEL_OWNER_ID) == owner_id
        and labels.get(LABEL_TENANT) == tenant
    )


# ---------------------------------------------------------------------------
# Conditional NetworkPolicy patches
# ---------------------------------------------------------------------------


def _guard(obj: dict) -> list:
    meta = obj.get("metadata") or {}
    return [
        {"op": "test", "path": "/metadata/uid", "value": meta.get("uid")},
        {"op": "test", "path": "/metadata/resourceVersion", "value": meta.get("resourceVersion")},
    ]


def cudn_evpn_rules_patch(policy: dict, rules: dict) -> dict:
    """Replace a group policy's rules and keep its membership.

    The patch tests uid and resourceVersion, so it applies only to the object
    that was read; a concurrent bind makes it fail and the task retries.
    """
    spec = (policy or {}).get("spec") or {}
    same = (
        (spec.get("ingress") or []) == rules["ingress"]
        and (spec.get("egress") or []) == rules["egress"]
        and spec.get("policyTypes") == ["Ingress", "Egress"]
    )
    if same:
        return {"changed": False, "patch": []}
    return {
        "changed": True,
        "patch": _guard(policy)
        + [
            {"op": "add", "path": "/spec/policyTypes", "value": ["Ingress", "Egress"]},
            {"op": "add", "path": "/spec/ingress", "value": rules["ingress"]},
            {"op": "add", "path": "/spec/egress", "value": rules["egress"]},
        ],
    }


def cudn_evpn_membership_patch(policy: dict, add: list | None = None, remove: list | None = None) -> dict:
    """Bind or unbind VMs on a group policy and keep its rules."""
    current = bound_ids(policy)
    wanted = sorted((set(current) | set(add or [])) - set(remove or []))
    selector = pod_selector(wanted)
    live_selector = ((policy or {}).get("spec") or {}).get("podSelector")
    if wanted == current and live_selector == selector:
        return {"changed": False, "patch": [], "bound": current}
    return {
        "changed": True,
        "patch": _guard(policy) + [{"op": "replace", "path": "/spec/podSelector", "value": selector}],
        "bound": wanted,
    }


class FilterModule:
    def filters(self) -> dict[str, Any]:
        return {
            "cudn_evpn_plan": cudn_evpn_plan,
            "cudn_evpn_vtep_state": cudn_evpn_vtep_state,
            "cudn_evpn_cudn_ready": cudn_evpn_cudn_ready,
            "cudn_evpn_cudn_assess": cudn_evpn_cudn_assess,
            "cudn_evpn_owned": cudn_evpn_owned,
            "cudn_evpn_rules_patch": cudn_evpn_rules_patch,
            "cudn_evpn_membership_patch": cudn_evpn_membership_patch,
        }
