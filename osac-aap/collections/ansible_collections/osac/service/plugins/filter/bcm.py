import re


BMC_TYPE_PATTERNS = {
    "redfish": re.compile(r"^rf\d+$"),
    "ipmi": re.compile(r"^ipmi\d+$"),
    "ilo": re.compile(r"^ilo\d+$"),
    "drac": re.compile(r"^drac\d+$"),
}

# BMC types that support Redfish and need system ID discovery via MAC matching.
# The protocol prefix is used instead of "redfish-virtualmedia".
REDFISH_COMPATIBLE_PROTOCOLS = {
    "redfish": "redfish-virtualmedia",
    "drac": "idrac-virtualmedia",
    "ilo": "ilo5-virtualmedia",
}

# BMC types with static URL formats (no Redfish discovery needed).
BMC_STATIC_URL_FORMATS = {
    "ipmi": "ipmi://{bmc_ip}",
}

_K8S_LABEL_RE = re.compile(r"[^a-zA-Z0-9._-]")


def classify_bmc_type(interface_name: str) -> str:
    for bmc_type, pattern in BMC_TYPE_PATTERNS.items():
        if pattern.match(interface_name):
            return bmc_type
    return "unknown"


def sanitize_k8s_name(name: str) -> str:
    """Converts a string to a valid Kubernetes DNS subdomain name."""
    sanitized = re.sub(r"[^a-z0-9.-]", "-", name.lower())
    sanitized = re.sub(r"\.{2,}", ".", sanitized)
    return sanitized[:253].strip(".-")


def sanitize_k8s_label_value(value: str) -> str:
    """Converts a string to a valid Kubernetes label value."""
    sanitized = _K8S_LABEL_RE.sub("-", value)
    return re.sub(r"^[^a-zA-Z0-9]+|[^a-zA-Z0-9]+$", "", sanitized[:63])


def parse_notes(notes: str) -> dict:
    """Parses key=value pairs from the BCM device notes field.

    Inverse of build_notes() in scripts/bcm_add_lite_nodes.py.
    """
    result = {}
    for line in (notes or "").splitlines():
        line = line.strip()
        if "=" in line:
            key, _, value = line.partition("=")
            result[key.strip()] = value.strip()
    return result


def bcm_device_to_server(device: dict,
                         racks_by_uuid: dict,
                         disable_bmc_cert_verification: bool = True) -> dict:
    """Converts a BCM device object into a normalized server dict for BMH creation.

    Resource class is read from the device notes field (resource_class=<value>).
    BMC type is classified from the interface name (rf0=redfish, ipmi0=ipmi, etc.).
    Hostname is sanitized for use as a Kubernetes resource name.
    """

    bmc_ip = ""
    bmc_type = "unknown"
    for iface in device.get("interfaces", []):
        if iface.get("childType") == "NetworkBmcInterface":
            bmc_ip = iface.get("ip", "")
            bmc_type = classify_bmc_type(iface.get("name", ""))
            break

    notes = parse_notes(device.get("notes", ""))
    resource_class = sanitize_k8s_label_value(
        notes.get("resource_class", "unknown"))

    rack_uuid = device.get("rackPosition", {}).get("rack", "")
    rack_name = sanitize_k8s_label_value(
        racks_by_uuid.get(rack_uuid, ""))

    bmc = device.get("bmcSettings") or {}

    return {
        "name": sanitize_k8s_name(device.get("hostname", "") or device.get("uuid", "")),
        "uuid": device.get("uuid", ""),
        "boot_mac": device.get("mac", ""),
        "bmc_ip": bmc_ip,
        "bmc_type": bmc_type,
        "bmc_username": bmc.get("userName", ""),
        "bmc_password": bmc.get("password", ""),
        "resource_class": resource_class,
        "rack": rack_name,
        "disable_bmc_cert_verification": bool(disable_bmc_cert_verification),
    }


def bcm_attach_bmc_urls(servers: list[dict],
                        mac_to_redfish: dict) -> list[dict]:
    """Attaches BMC URLs to servers based on their bmc_type.

    Redfish-compatible types (redfish, drac, ilo) use MAC-based system
    discovery with their respective protocol prefix.
    Static types (ipmi) get a simple URL from BMC_STATIC_URL_FORMATS.
    Servers with empty boot_mac or bmc_ip get an empty bmc_url.
    """
    result = []
    for server in servers:
        bmc_type = server.get("bmc_type", "unknown")
        bmc_url = ""

        if not server.get("boot_mac") or not server.get("bmc_ip"):
            result.append({**server, "bmc_url": bmc_url})
            continue

        if bmc_type in REDFISH_COMPATIBLE_PROTOCOLS:
            mac = server["boot_mac"].lower()
            entry = mac_to_redfish.get(mac, {})
            if entry.get("system_path"):
                bmc_ip = entry.get("bmc_ip", server["bmc_ip"])
                protocol = REDFISH_COMPATIBLE_PROTOCOLS[bmc_type]
                bmc_url = f"{protocol}+https://{bmc_ip}{entry['system_path']}"
        elif bmc_type in BMC_STATIC_URL_FORMATS:
            bmc_url = BMC_STATIC_URL_FORMATS[bmc_type].format(bmc_ip=server["bmc_ip"])

        result.append({**server, "bmc_url": bmc_url})
    return result


def bcm_build_redfish_queries(servers: list[dict],
                              redfish_results: list[dict]) -> list[dict]:
    """Builds a flat list of Redfish EthernetInterface queries from system collections.

    Correlates servers to results by positional index — relies on Ansible's
    loop+register producing .results in the same order as the input list.
    """
    queries = []
    for i, result in enumerate(redfish_results):
        if result.get("skipped") or result.get("failed"):
            continue
        server = servers[i]
        members = result.get("json", {}).get("Members", [])
        for member in members:
            odata_id = member.get("@odata.id")
            if not odata_id:
                continue
            queries.append({
                "bmc_ip": server["bmc_ip"],
                "bmc_username": server["bmc_username"],
                "bmc_password": server["bmc_password"],
                "disable_bmc_cert_verification": server.get("disable_bmc_cert_verification", True),
                "system_path": odata_id,
            })
    return queries


def bcm_build_eth_iface_queries(eth_collection_results: list[dict]) -> list[dict]:
    """Expands EthernetInterfaces collection responses into individual interface queries.

    Takes registered results from fetching /EthernetInterfaces collections
    and produces a flat list of individual interface URLs to fetch, along
    with the parent system path and BMC credentials.
    """
    queries = []
    for result in eth_collection_results:
        if result.get("skipped") or result.get("failed"):
            continue
        query = result.get("query", {})
        members = result.get("json", {}).get("Members", [])
        for member in members:
            odata_id = member.get("@odata.id")
            if not odata_id:
                continue
            queries.append({
                "bmc_ip": query.get("bmc_ip", ""),
                "bmc_username": query.get("bmc_username", ""),
                "bmc_password": query.get("bmc_password", ""),
                "disable_bmc_cert_verification": query.get("disable_bmc_cert_verification", True),
                "system_path": query.get("system_path", ""),
                "iface_path": odata_id,
            })
    return queries


def bcm_build_mac_to_redfish(iface_results: list[dict]) -> dict:
    """Builds MAC-to-Redfish-system mapping from individual EthernetInterface responses.

    Reads the MACAddress property from each fetched interface and maps it
    to the parent system path and BMC IP.
    """
    mapping = {}
    for result in iface_results:
        if result.get("skipped") or result.get("failed"):
            continue
        query = result.get("query", {})
        mac = result.get("json", {}).get("MACAddress", "")
        if mac:
            mapping[mac.lower()] = {
                "bmc_ip": query.get("bmc_ip", ""),
                "system_path": query.get("system_path", ""),
            }
    return mapping


def bcm_redfish_compatible_types(_input=None) -> list[str]:
    """Returns list of BMC types that need Redfish discovery."""
    return list(REDFISH_COMPATIBLE_PROTOCOLS.keys())


class FilterModule:
    def filters(self):
        return {
            "bcm_device_to_server": bcm_device_to_server,
            "bcm_attach_bmc_urls": bcm_attach_bmc_urls,
            "bcm_build_redfish_queries": bcm_build_redfish_queries,
            "bcm_build_eth_iface_queries": bcm_build_eth_iface_queries,
            "bcm_build_mac_to_redfish": bcm_build_mac_to_redfish,
            "bcm_redfish_compatible_types": bcm_redfish_compatible_types,
        }
