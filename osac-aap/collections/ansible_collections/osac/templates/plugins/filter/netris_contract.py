"""Pure logic of the shared helpers of the Netris Fabric Manager role.

The helper task files of roles/netris (_settings, _input, _names, _publish)
only call these filters, so the contract rules can be tested without Netris:

- ``netris_contract_settings_error``: checks the NETRIS_* settings a task needs.
- ``netris_contract_input``: checks osac_job_vars and returns the API IDs.
- ``netris_contract_names``: the only place that builds Netris object names.
- ``netris_contract_result``: the only place that shapes ``osac_result``.

Every filter returns data; a bad input is reported in the return value so a
task can tell a final error from a transient one.
"""

from __future__ import annotations

import ipaddress
import json
import re
from typing import Any
from urllib.parse import urlsplit

_UUID = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")

SETTINGS_PREFIX = "Missing or malformed setting in network-fulfillment-ig: "

# Setting groups a task can require, and the NETRIS_* variables they check.
SETTING_GROUPS = (
    "controller",
    "credentials",
    "site",
    "tenant",
    "mgmt_vpc",
    "bm_provisioning_vnet",
    "resource_class_map",
)

NAME_PREFIXES = {
    "vpc": "osac-vn-",
    "vn_ipam": "osac-vn-",
    "vnet": "osac-sn-",
    "subnet_ipam": "osac-sn-",
    "pool_ipam": "osac-pool-",
    "natgw_snat": "osac-natgw-",
    "eipa_dnat": "osac-eipa-",
    "server_cluster": "osac-fd-",
    "eip_reservation": "osac-eip-",
}

PARENTS = {"virtual_network": "VirtualNetwork", "subnet": "Subnet", "external_ip_pool": "ExternalIPPool"}

RESULT_KEYS = ("backend_id", "l3_vni", "network_segment", "external_ip", "dhcp_lease")



def _int(value: Any, name: str) -> int:
    if isinstance(value, bool):
        raise ValueError(f"{name} must be an integer, not a boolean")
    return int(value)


def _positive_int(value: Any) -> bool:
    return isinstance(value, int) and not isinstance(value, bool) and value > 0


def _ipv4_network_ok(value: Any) -> bool:
    if not isinstance(value, str):
        return False
    try:
        net = ipaddress.ip_network(value, strict=True)
    except ValueError:
        return False
    return net.version == 4 and str(net) == value


def _class_map_ok(value: Any) -> bool:
    # Ansible turns an environment value that looks like JSON into a mapping.
    if isinstance(value, dict):
        return bool(value)
    if not isinstance(value, str) or not value.strip():
        return False
    try:
        parsed = json.loads(value)
    except ValueError:
        return False
    return isinstance(parsed, dict) and bool(parsed)


def netris_contract_settings_error(settings: Any, required: Any) -> str:
    """Return "" when the required setting groups are valid, else one message.

    ``settings`` holds controller_url, username, has_password, site_id,
    tenant_id, mgmt_vpc_id, bm_provisioning_vnet
    and resource_class_map (JSON text or the parsed mapping). The password is never passed
    in; only whether it is set. The message names the NETRIS_* variables.
    """
    settings = settings if isinstance(settings, dict) else {}
    problems: list[str] = []
    for group in required or []:
        if group not in SETTING_GROUPS:
            raise ValueError(f"unknown Netris setting group {group}")
        if group == "controller":
            url = settings.get("controller_url")
            parts = urlsplit(url) if isinstance(url, str) else None
            if not parts or parts.scheme not in ("http", "https") or not parts.hostname:
                problems.append("NETRIS_CONTROLLER_URL must be an http(s) URL")
        elif group == "credentials":
            if not (isinstance(settings.get("username"), str) and settings["username"].strip()):
                problems.append("NETRIS_USERNAME must be set")
            if settings.get("has_password") is not True:
                problems.append("NETRIS_PASSWORD must be set")
        elif group in ("site", "tenant", "mgmt_vpc"):
            variable = {"site": "NETRIS_SITE_ID", "tenant": "NETRIS_TENANT_ID", "mgmt_vpc": "NETRIS_MGMT_VPC_ID"}[group]
            if not _positive_int(settings.get(f"{group}_id")):
                problems.append(f"{variable} must be a positive integer")
        elif group == "bm_provisioning_vnet":
            value = settings.get("bm_provisioning_vnet")
            if not (isinstance(value, str) and value.strip()):
                problems.append("NETRIS_BM_PROVISIONING_VNET must be the name of a V-Net")
        elif group == "resource_class_map":
            if not _class_map_ok(settings.get("resource_class_map")):
                problems.append("NETRIS_RESOURCE_CLASS_MAP must be a non-empty JSON object")
    if not problems:
        return ""
    return SETTINGS_PREFIX + "; ".join(problems) + ". OSAC retries the task."


def _api_id(value: Any) -> bool:
    return isinstance(value, str) and bool(_UUID.match(value))


def netris_contract_input(job: Any, require: Any = None) -> dict:
    """Check the task input and return the API IDs the task works with.

    ``require`` lists the parents the task needs: virtual_network, subnet,
    external_ip_pool. A parent that is present is always checked. When the
    resource is itself a VirtualNetwork, Subnet or ExternalIPPool its ID also
    fills the matching ``*_id``. A failure is final.
    """
    out = {
        "error": None,
        "resource_id": "",
        "resource_kind": "",
        "virtual_network_id": "",
        "subnet_id": "",
        "external_ip_pool_id": "",
    }

    def fail(message: str) -> dict:
        out["error"] = f"Netris invalid task input: {message}"
        return out

    job = job if isinstance(job, dict) else {}
    resource = job.get("resource")
    if not isinstance(resource, dict):
        return fail("resource must be a mapping")
    kind = resource.get("kind")
    if not isinstance(kind, str) or not kind:
        return fail("resource.kind must be a non-empty string")
    if not _api_id(resource.get("id")):
        return fail("resource.id must be a lowercase UUID API ID")
    out["resource_id"], out["resource_kind"] = resource["id"], kind
    for key, parent_kind in PARENTS.items():
        snap = job.get(key)
        if snap is None:
            if key in (require or []) and kind != parent_kind:
                return fail(f"{key} is required")
        elif not isinstance(snap, dict) or not _api_id(snap.get("id")):
            return fail(f"{key}.id must be a lowercase UUID API ID")
        else:
            out[f"{key}_id"] = snap["id"]
        if kind == parent_kind and not out[f"{key}_id"]:
            out[f"{key}_id"] = resource["id"]
    if kind == "ExternalIPAttachment":
        # The DNAT rule lives in the VPC of the destination VirtualNetwork.
        domain = ((job.get("destination") or {}).get("routing_domain") or {}) if isinstance(job.get("destination"), dict) else {}
        if isinstance(domain, dict) and domain.get("type") == "tenant_virtual_network":
            if not _api_id(domain.get("virtual_network_id")):
                return fail("destination.routing_domain.virtual_network_id must be a lowercase UUID API ID")
            out["virtual_network_id"] = domain["virtual_network_id"]
    return out


def netris_network_type_error(job: Any) -> str:
    """Return "" for no network_type or evpn, else a final error message."""
    value = (job or {}).get("network_type") if isinstance(job, dict) else None
    if value is None or value == "evpn":
        return ""
    return f"Netris supports network_type evpn only, not {str(value)[:40]!r}"


def netris_contract_names(ids: Any) -> dict:
    """Netris object names from the API IDs of ``netris_contract_input`` (D-7).

    A name whose source ID is unknown is "". The object kinds and their IDs:
    vpc and vn_ipam from the VirtualNetwork, vnet and subnet_ipam from the
    Subnet, pool_ipam from the ExternalIPPool,
    natgw_snat / eipa_dnat / server_cluster / eip_reservation from the task's
    resource.
    """
    ids = ids if isinstance(ids, dict) else {}
    source = {
        "vpc": ids.get("virtual_network_id"),
        "vn_ipam": ids.get("virtual_network_id"),
        "vnet": ids.get("subnet_id"),
        "subnet_ipam": ids.get("subnet_id"),
        "pool_ipam": ids.get("external_ip_pool_id"),
        "natgw_snat": ids.get("resource_id"),
        "eipa_dnat": ids.get("resource_id"),
        "server_cluster": ids.get("resource_id"),
        "eip_reservation": ids.get("resource_id"),
    }
    return {key: (NAME_PREFIXES[key] + value if _api_id(value) else "") for key, value in source.items()}


def netris_contract_result(raw: Any) -> dict:
    """Shape an osac_result: integers as int, backend_id as a string.

    Keys with a None or empty value are dropped. Any key other than
    backend_id, l3_vni, network_segment {type, l2_vni}, external_ip {address}
    and dhcp_lease {ip_address, mac_address} raises ValueError.
    """
    raw = raw if isinstance(raw, dict) else {}
    unknown = sorted(set(raw) - set(RESULT_KEYS))
    if unknown:
        raise ValueError(f"unknown osac_result keys: {', '.join(unknown)}")
    out: dict = {}
    if raw.get("backend_id") not in (None, ""):
        out["backend_id"] = str(raw["backend_id"])
    if raw.get("l3_vni") not in (None, ""):
        out["l3_vni"] = _int(raw["l3_vni"], "l3_vni")
    segment = raw.get("network_segment")
    if segment:
        if not isinstance(segment, dict) or segment.get("l2_vni") in (None, ""):
            raise ValueError("network_segment needs type and l2_vni")
        out["network_segment"] = {"type": str(segment["type"]), "l2_vni": _int(segment["l2_vni"], "l2_vni")}
    external = raw.get("external_ip")
    if external:
        out["external_ip"] = {"address": str(external["address"])}
    lease = raw.get("dhcp_lease")
    if lease:
        out["dhcp_lease"] = {"ip_address": str(lease["ip_address"]), "mac_address": str(lease["mac_address"])}
    return out


GATEWAY_MODES = ("gateway_ipv4", "none", "physical_first")


def _plan(error: str = "", gateway: str = "", start: str = "", end: str = "") -> dict:
    return {"error": error, "gateway": gateway, "dhcp_start": start, "dhcp_end": end}


def netris_vn_cidr_error(cidr: Any) -> str:
    """Return "" for an empty or canonical IPv4 CIDR, else a final error."""
    if cidr in (None, ""):
        return ""
    if not _ipv4_network_ok(cidr):
        return f"VirtualNetwork spec.ipv4Cidr {str(cidr)[:40]!r} must be a canonical IPv4 CIDR"
    return ""


def _dhcp_range(net: Any, gateway: Any, physical: list) -> tuple | None:
    """The DHCP range over the physical blocks: after the gateway, below broadcast."""
    if not physical:
        return None
    start = max(min(b.network_address for b in physical), gateway + 1)
    end = min(max(b.broadcast_address for b in physical), net.broadcast_address - 1)
    return (start, end) if start <= end else None


def netris_vnet_plan(job: Any, mode: Any = "gateway_ipv4") -> dict:
    """Plan the gateway and DHCP range of the V-Net of a Subnet.

    Returns {error, gateway, dhcp_start, dhcp_end}; "" means none. With a
    network type DHCP is always off (OVN-K assigns the VM addresses) and
    ``mode`` picks the gateway: gateway_ipv4 (ipam.gateway_ipv4/prefix, the
    default), none, or physical_first (first address of the first
    ipam.physical_ipv4_cidrs block). Without a network type the gateway is
    ipam.gateway_ipv4/prefix and DHCP serves the physical blocks.
    """
    job = job if isinstance(job, dict) else {}
    spec = (job.get("resource") or {}).get("spec") or {}
    ipam = job.get("ipam") if isinstance(job.get("ipam"), dict) else {}
    cidr = spec.get("ipv4Cidr")
    if not _ipv4_network_ok(cidr):
        return _plan("Subnet spec.ipv4Cidr must be a canonical IPv4 CIDR")
    net = ipaddress.ip_network(cidr)
    evpn = job.get("network_type") == "evpn"
    if evpn and mode not in GATEWAY_MODES:
        return _plan(f"netris_evpn_vnet_gateway_mode must be one of {', '.join(GATEWAY_MODES)}, not {str(mode)[:30]!r}")
    physical = []
    for block in ipam.get("physical_ipv4_cidrs") or []:
        if not _ipv4_network_ok(block) or not ipaddress.ip_network(block).subnet_of(net):
            return _plan(f"ipam.physical_ipv4_cidrs {str(block)[:40]!r} is not an IPv4 block inside the Subnet CIDR")
        physical.append(ipaddress.ip_network(block))
    if evpn and mode == "none":
        return _plan()
    if evpn and mode == "physical_first":
        if not physical:
            return _plan("ipam.physical_ipv4_cidrs is empty; the physical_first gateway needs a block")
        first = next(physical[0].hosts())
        if first in (net.network_address, net.broadcast_address):
            return _plan("the first host address of ipam.physical_ipv4_cidrs[0] is the Subnet network or broadcast address")
        return _plan(gateway=f"{first}/{net.prefixlen}")
    try:
        gateway = ipaddress.ip_address(ipam.get("gateway_ipv4"))
    except ValueError:
        return _plan("ipam.gateway_ipv4 must be an IPv4 address")
    if gateway.version != 4 or gateway not in net:
        return _plan("ipam.gateway_ipv4 must be an IPv4 address inside the Subnet CIDR")
    if gateway in (net.network_address, net.broadcast_address):
        return _plan("ipam.gateway_ipv4 must not be the Subnet network or broadcast address")
    gateway_cidr = f"{gateway}/{net.prefixlen}"
    if evpn or not physical:
        return _plan(gateway=gateway_cidr)
    rng = _dhcp_range(net, gateway, physical)
    if rng is None:
        return _plan(gateway=gateway_cidr)
    return _plan(gateway=gateway_cidr, start=str(rng[0]), end=str(rng[1]))


def netris_vni_check(value: Any, what: str = "object") -> dict:
    """Check a VNI read from Netris: {vni, error, retryable}.

    Missing or 0 is retryable (Netris has not assigned it yet); anything
    outside 1..16777215 or not a whole number is final.
    """
    if value is None or (isinstance(value, str) and not value.strip()):
        return {"vni": 0, "error": f"Netris has not assigned the VNI yet ({what})", "retryable": True}
    if isinstance(value, bool) or not re.fullmatch(r"-?\d+", str(value).strip()):
        return {"vni": 0, "error": f"Netris returned an unusable VNI for the {what}: {str(value)[:20]!r}", "retryable": False}
    number = int(str(value).strip())
    if number == 0:
        return {"vni": 0, "error": f"Netris has not assigned the VNI yet ({what})", "retryable": True}
    if not 1 <= number <= 16777215:
        return {"vni": 0, "error": f"Netris returned a VNI outside 1..16777215 for the {what}: {number}", "retryable": False}
    return {"vni": number, "error": "", "retryable": False}


def _dhcp_values(gateway: dict) -> tuple[str, str]:
    nested = gateway.get("dhcp") if isinstance(gateway.get("dhcp"), dict) else {}
    start = nested.get("start") or gateway.get("dhcpStartIp") or ""
    end = nested.get("end") or gateway.get("dhcpEndIp") or ""
    return str(start).strip(), str(end).strip()


def netris_vnet_gateway_update(detail: Any, plan: Any, keep_dhcp: bool = False) -> dict:
    """Compare a V-Net read with a plan: {matches, body}.

    ``body`` is the full V-Net to PUT: the read detail with ``gateways``
    replaced by the planned gateway. The returned gateway keeps the fields of
    the existing one (ID, nested Netris 4.16 ``dhcp`` metadata) and only sets
    prefix, dhcpEnabled and the DHCP range. With ``keep_dhcp`` the DHCP of the
    V-Net is not compared and not changed (a Subnet with a network type: a
    bare-metal attachment may have switched DHCP on).
    """
    detail = detail if isinstance(detail, dict) else {}
    plan = plan if isinstance(plan, dict) else {}
    gateways = detail.get("gateways") or []
    want, start, end = plan.get("gateway") or "", plan.get("dhcp_start") or "", plan.get("dhcp_end") or ""
    dhcp = bool(start)
    if not want:
        return {"matches": not gateways, "body": {**detail, "gateways": []}}
    existing = gateways[0] if gateways and isinstance(gateways[0], dict) else {}
    if keep_dhcp:
        matches = len(gateways) == 1 and str(existing.get("prefix", "")).strip() == want
        gateway = {**existing, "prefix": want}
        gateway.setdefault("dhcpEnabled", False)
        return {"matches": matches, "body": {**detail, "gateways": [gateway]}}
    matches = (
        len(gateways) == 1
        and str(existing.get("prefix", "")).strip() == want
        and bool(existing.get("dhcpEnabled", False)) is dhcp
        and (not dhcp or _dhcp_values(existing) == (start, end))
    )
    new = {**existing, "prefix": want, "dhcpEnabled": dhcp}
    if dhcp:
        if isinstance(existing.get("dhcp"), dict):
            new["dhcp"] = {**existing["dhcp"], "start": start, "end": end}
        else:
            new["dhcpStartIp"], new["dhcpEndIp"] = start, end
    return {"matches": matches, "body": {**detail, "gateways": [new]}}


MAC_RE = re.compile(r"^[0-9a-fA-F]{2}(:[0-9a-fA-F]{2}){5}$")
NO_DHCP_RANGE = "no fabric address range for bare-metal DHCP"


def netris_attachment_plan(job: Any) -> dict:
    """Check the interface of a NetworkAttachment task: {error, kind, ...}.

    ``kind`` is overlay (a documented no-op) or physical. A physical
    attachment also returns host_name, port (the interface name) and mac
    (lowercase). Every error is final.
    """
    job = job if isinstance(job, dict) else {}
    attachment = job.get("network_attachment") if isinstance(job.get("network_attachment"), dict) else {}
    interface = attachment.get("interface") if isinstance(attachment.get("interface"), dict) else {}
    kind = interface.get("type")
    if kind == "overlay":
        return {"error": "", "kind": "overlay"}

    def bad(message: str) -> dict:
        return {"error": f"Netris invalid task input: {message}", "kind": ""}

    if kind != "physical":
        return bad(f"network_attachment.interface.type must be overlay or physical, not {str(kind)[:20]!r}")
    subnet = job.get("subnet") if isinstance(job.get("subnet"), dict) else {}
    if not _api_id(attachment.get("subnet")) or attachment.get("subnet") != subnet.get("id"):
        return bad("network_attachment.subnet must equal subnet.id")
    target = job.get("target") if isinstance(job.get("target"), dict) else {}
    host = target.get("host_name")
    name = interface.get("name")
    mac = interface.get("mac_address")
    if not isinstance(host, str) or not host.strip() or len(host) > 253:
        return bad("target.host_name must be a non-empty host name")
    if not isinstance(name, str) or not name.strip() or len(name) > 64:
        return bad("network_attachment.interface.name must be a non-empty interface name")
    if not isinstance(mac, str) or not MAC_RE.match(mac):
        return bad("network_attachment.interface.mac_address must be a MAC address like aa:bb:cc:dd:ee:ff")
    return {"error": "", "kind": "physical", "host_name": host, "port": name, "mac": mac.lower()}


def netris_vnet_dhcp_update(detail: Any, job: Any) -> dict:
    """Plan DHCP over ipam.physical_ipv4_cidrs on an existing V-Net.

    Returns {error, matches, body, start, end}. The gateway of the V-Net is
    kept as it is (its address starts the range, like on a Subnet without a
    network type); ``body`` is the full V-Net to PUT. An empty or unusable
    physical range, or a V-Net without a gateway, is an error.
    """
    detail = detail if isinstance(detail, dict) else {}
    job = job if isinstance(job, dict) else {}

    def bad(message: str) -> dict:
        return {"error": message, "matches": False, "body": {}, "start": "", "end": ""}

    spec = (job.get("subnet") or {}).get("spec") or {}
    cidr = spec.get("ipv4Cidr") or ((job.get("resource") or {}).get("spec") or {}).get("ipv4Cidr")
    if not _ipv4_network_ok(cidr):
        return bad("Subnet spec.ipv4Cidr must be a canonical IPv4 CIDR")
    net = ipaddress.ip_network(cidr)
    ipam = job.get("ipam") if isinstance(job.get("ipam"), dict) else {}
    physical = []
    for block in ipam.get("physical_ipv4_cidrs") or []:
        if not _ipv4_network_ok(block) or not ipaddress.ip_network(block).subnet_of(net):
            return bad(f"ipam.physical_ipv4_cidrs {str(block)[:40]!r} is not an IPv4 block inside the Subnet CIDR")
        physical.append(ipaddress.ip_network(block))
    if not physical:
        return bad(NO_DHCP_RANGE)
    gateways = detail.get("gateways") or []
    existing = gateways[0] if gateways and isinstance(gateways[0], dict) else {}
    try:
        gateway = ipaddress.ip_interface(str(existing.get("prefix", "")))
    except ValueError:
        return bad("the V-Net has no gateway to serve DHCP from")
    if gateway.version != 4 or gateway.ip not in net:
        return bad("the gateway of the V-Net is not an IPv4 address inside the Subnet CIDR")
    rng = _dhcp_range(net, gateway.ip, physical)
    if rng is None:
        return bad(NO_DHCP_RANGE)
    start, end = str(rng[0]), str(rng[1])
    update = netris_vnet_gateway_update(detail, {"gateway": str(existing["prefix"]).strip(), "dhcp_start": start, "dhcp_end": end})
    return {"error": "", "matches": update["matches"], "body": update["body"], "start": start, "end": end}


def netris_dhcp_lease(hosts: Any, mac: Any) -> dict:
    """The IP of the one IPAM host whose MAC is ``mac``: {error, ip_address}.

    Netris stores lease MACs in lowercase, so the comparison ignores case. No
    match, or more than one, is an error (OSAC retries the query).
    """
    if not isinstance(hosts, list):
        return {"error": "Netris returned no list of IPAM hosts", "ip_address": ""}
    want = str(mac).lower()
    found = []
    for host in hosts:
        if not isinstance(host, dict):
            continue
        macs = [str(m.get("address", "")).lower() for m in host.get("mac") or [] if isinstance(m, dict)]
        if want in macs:
            found.append(str(host.get("address", "")).split("/")[0])
    if not found:
        return {"error": "no DHCP lease for the interface MAC address yet", "ip_address": ""}
    if len(found) > 1:
        return {"error": "more than one IPAM host has the interface MAC address", "ip_address": ""}
    return {"error": "", "ip_address": found[0]}


def netris_owned_pick(items: Any, name: Any, vpc_id: Any, tenant_id: Any) -> dict:
    """Pick the Netris object of ``name`` that belongs to this VPC and tenant.

    Returns {error, id, unverified}. ``id`` is "" when no object has the name.
    A list entry that omits its VPC or tenant is picked only when nothing
    proven ours exists; the caller must then read the object and check it
    with ``netris_owner_error`` (``unverified`` is true). Same-name objects of
    another VPC or tenant are never picked: with no pick, that is an error.
    """
    same = [i for i in items or [] if isinstance(i, dict) and i.get("name") == name]
    vpc, tenant = int(vpc_id), int(tenant_id)

    def owner(item: dict) -> tuple[Any, Any]:
        return (item.get("vpc") or {}).get("id"), (item.get("tenant") or {}).get("id")

    unknown = [i for i in same if None in owner(i)]
    known = [i for i in same if i not in unknown]
    ours = [i for i in known if owner(i) == (vpc, tenant)]
    if len(ours) > 1:
        return {"error": f"more than one Netris object named {name} is ambiguous; refusing to pick one", "id": "", "unverified": False}
    if ours:
        return {"error": "", "id": ours[0]["id"], "unverified": False}
    if len(unknown) > 1:
        return {"error": f"more than one Netris object named {name} is ambiguous; refusing to pick one", "id": "", "unverified": False}
    if unknown:
        return {"error": "", "id": unknown[0]["id"], "unverified": True}
    if known:
        return {"error": f"Netris object {name} already exists in another tenant or VPC; refusing to use it", "id": "", "unverified": False}
    return {"error": "", "id": "", "unverified": False}


def netris_owner_error(obj: Any, vpc_id: Any, tenant_id: Any) -> str:
    """"" when a Netris object read by ID is in this VPC and tenant, else a message."""
    obj = obj if isinstance(obj, dict) else {}
    vpc, tenant = (obj.get("vpc") or {}).get("id"), (obj.get("tenant") or {}).get("id")
    if vpc is None or tenant is None:
        return "its VPC or tenant is not reported; refusing to use it"
    if (vpc, tenant) != (int(vpc_id), int(tenant_id)):
        return "it belongs to another tenant or VPC; refusing to use it"
    return ""


def netris_fabric_domain_template(class_map: Any) -> dict:
    """The Server Cluster template ID of a FabricDomain: {template_id, error}.

    A FabricDomain carries no resource class, so the class map must name exactly
    one distinct server_cluster_template_id.
    """
    if isinstance(class_map, str):
        try:
            class_map = json.loads(class_map)
        except ValueError:
            class_map = None
    if not isinstance(class_map, dict):
        return {"template_id": 0, "error": "NETRIS_RESOURCE_CLASS_MAP must be a JSON object with a server_cluster_template_id"}
    found: set[int] = set()
    for entry in class_map.values():
        value = entry.get("server_cluster_template_id") if isinstance(entry, dict) else None
        if value is None:
            continue
        if isinstance(value, bool) or not isinstance(value, (int, str)) or not str(value).isdigit() or int(value) <= 0:
            return {"template_id": 0, "error": "NETRIS_RESOURCE_CLASS_MAP has a server_cluster_template_id that is not a positive integer"}
        found.add(int(value))
    if len(found) != 1:
        return {
            "template_id": 0,
            "error": (
                "NETRIS_RESOURCE_CLASS_MAP must name exactly one distinct server_cluster_template_id "
                f"for a FabricDomain, found {len(found)}"
            ),
        }
    return {"template_id": found.pop(), "error": ""}


def netris_fabric_domain_servers_error(job: Any) -> str:
    """Return "" when resource.spec.servers is a non-empty list of unique non-empty names, else a final error."""
    resource = job.get("resource") if isinstance(job, dict) else None
    spec = resource.get("spec") if isinstance(resource, dict) else None
    servers = spec.get("servers") if isinstance(spec, dict) else None
    prefix = "Netris invalid task input: resource.spec.servers "
    if not isinstance(servers, list) or not servers:
        return prefix + "must be a non-empty list of host names"
    if any(not isinstance(name, str) or not name.strip() or name != name.strip() for name in servers):
        return prefix + "must contain only non-empty host names"
    if len(set(servers)) != len(servers):
        return prefix + "must not repeat a host name"
    return ""
def _ipv4_address_ok(value: Any) -> bool:
    if not isinstance(value, str) or "/" in value or value != value.strip():
        return False
    try:
        return isinstance(ipaddress.ip_address(value), ipaddress.IPv4Address)
    except ValueError:
        return False


def netris_pool_plan(job: Any) -> dict:
    """Return {error, cidr} for an ExternalIPPool: spec.cidrs holds exactly one CIDR."""
    resource = (job or {}).get("resource") if isinstance(job, dict) else None
    spec = resource.get("spec") if isinstance(resource, dict) else None
    cidrs = spec.get("cidrs") if isinstance(spec, dict) else None
    if not isinstance(cidrs, list) or len(cidrs) != 1 or not _ipv4_network_ok(cidrs[0]):
        return {"error": "Netris invalid task input: resource.spec.cidrs must hold exactly one canonical IPv4 CIDR", "cidr": ""}
    return {"error": "", "cidr": cidrs[0]}


def netris_nat_plan(job: Any) -> dict:
    """Plan the NAT rule of an ExternalIPAttachment (DNAT) or NATGateway (SNAT).

    Returns {error, action, external, internal, source, domain}; "" means none.
    DNAT sends ``external_ip.address`` to ``destination.address`` in the routing
    domain of the destination; SNAT sends the VirtualNetwork's spec.ipv4Cidr to
    ``external_ip.address`` in the VPC of the VirtualNetwork. A bad input is a
    final error.
    """

    def plan(error: str = "", action: str = "", external: str = "", internal: str = "", source: str = "",
             domain: str = "") -> dict:
        text = f"Netris invalid task input: {error}" if error else ""
        return {"error": text, "action": action, "external": external, "internal": internal,
                "source": source, "domain": domain}

    job = job if isinstance(job, dict) else {}
    kind = (job.get("resource") or {}).get("kind") if isinstance(job.get("resource"), dict) else None
    external_ip = job.get("external_ip")
    external = external_ip.get("address") if isinstance(external_ip, dict) else None
    if kind not in ("ExternalIPAttachment", "NATGateway"):
        return plan("resource.kind must be ExternalIPAttachment or NATGateway")
    if not _ipv4_address_ok(external):
        return plan("external_ip.address must be an IPv4 address")
    if kind == "NATGateway":
        vn = job.get("virtual_network")
        cidr = ((vn.get("spec") or {}).get("ipv4Cidr") if isinstance(vn, dict) and isinstance(vn.get("spec"), dict) else None)
        if not _ipv4_network_ok(cidr):
            return plan("virtual_network.spec.ipv4Cidr must be a canonical IPv4 CIDR")
        return plan(action="snat", external=external, source=cidr, domain="tenant_virtual_network")
    destination = job.get("destination")
    if not isinstance(destination, dict) or not _ipv4_address_ok(destination.get("address")):
        return plan("destination.address must be an IPv4 address")
    domain = destination.get("routing_domain")
    domain_type = domain.get("type") if isinstance(domain, dict) else None
    if domain_type not in ("tenant_virtual_network", "provider_management"):
        return plan("destination.routing_domain.type must be tenant_virtual_network or provider_management")
    if domain_type == "tenant_virtual_network" and not _api_id(domain.get("virtual_network_id")):
        return plan("destination.routing_domain.virtual_network_id must be a lowercase UUID API ID")
    return plan(action="dnat", external=external, internal=destination["address"], domain=domain_type)


class FilterModule:
    def filters(self) -> dict[str, Any]:
        return {
            "netris_contract_settings_error": netris_contract_settings_error,
            "netris_contract_input": netris_contract_input,
            "netris_network_type_error": netris_network_type_error,
            "netris_contract_names": netris_contract_names,
            "netris_contract_result": netris_contract_result,
            "netris_vn_cidr_error": netris_vn_cidr_error,
            "netris_vnet_plan": netris_vnet_plan,
            "netris_vni_check": netris_vni_check,
            "netris_vnet_gateway_update": netris_vnet_gateway_update,
            "netris_attachment_plan": netris_attachment_plan,
            "netris_vnet_dhcp_update": netris_vnet_dhcp_update,
            "netris_dhcp_lease": netris_dhcp_lease,
            "netris_owned_pick": netris_owned_pick,
            "netris_owner_error": netris_owner_error,
            "netris_fabric_domain_template": netris_fabric_domain_template,
            "netris_fabric_domain_servers_error": netris_fabric_domain_servers_error,
            "netris_nat_plan": netris_nat_plan,
            "netris_pool_plan": netris_pool_plan,
        }
