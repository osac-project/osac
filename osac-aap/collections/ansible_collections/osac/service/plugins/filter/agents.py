def mac_to_agent_name(v: list[str], agents) -> str | None:
    """Returns the name of the agent with matching MAC address"""

    target_addresses = set(v)
    for agent in agents:
        # Use set intersection
        interfaces = agent.get("status", {}) \
                          .get("inventory", {}) \
                          .get("interfaces", [])
        agent_addresses = {iface["macAddress"] for iface in interfaces}

        if target_addresses.intersection(agent_addresses):
            return agent['metadata']['name']

    return None


def agent_vpc_interfaces(agent, interface_names):
    """Return [{name, macAddress}] for agent interfaces matching the given names."""
    names = set(interface_names)
    interfaces = agent.get("status", {}).get("inventory", {}).get("interfaces", [])
    return [{"name": iface["name"], "macAddress": iface["macAddress"]}
            for iface in interfaces if iface["name"] in names]


def agent_mgmt_ip(agent, mgmt_interface_name):
    """Return the first IPv4 address of the management interface for an agent."""
    interfaces = agent.get("status", {}).get("inventory", {}).get("interfaces", [])
    for iface in interfaces:
        if iface.get("name") == mgmt_interface_name:
            addrs = iface.get("ipV4Addresses", [])
            if addrs:
                # ipV4Addresses entries may include CIDR prefix, strip it
                return addrs[0].split("/")[0]
    return None


def agent_network_interfaces(agent, selected_macs):
    """Return an Agent's OS interfaces and its selected fabric interface.

    The Agent inventory is the authoritative mapping from provider MACs to
    names such as ens6 or enp1s0.  Callers must supply the selected physical
    MAC(s) from the NetworkAttachment/BareMetalInstance status rather than
    assuming an interface name or PCI ordering.
    """
    interfaces = agent.get("status", {}).get("inventory", {}).get("interfaces", [])
    normalized = {str(mac).lower().replace("-", ":") for mac in selected_macs}
    result = []
    for interface in interfaces:
        name = interface.get("name")
        mac = interface.get("macAddress")
        if not name or not mac:
            continue
        item = {
            "name": name,
            "macAddress": mac,
            "selected": str(mac).lower().replace("-", ":") in normalized,
        }
        result.append(item)
    return result


def agents_matching_selected_macs(agents, selected_macs):
    """Return only Agents with an inventory interface for a selected MAC.

    Assisted keeps disconnected Agents in an InfraEnv after a failed or
    replaced attempt.  Those historical objects must not block the current
    worker from receiving its MAC-driven NMStateConfig.
    """
    return [
        agent
        for agent in agents or []
        if sum(
            1
            for interface in agent_network_interfaces(agent, selected_macs)
            if interface["selected"]
        )
        == 1
    ]


def agent_nmstate_interfaces(agent, selected_macs):
    """Return NMStateConfig interface selectors for an Agent inventory."""
    return [
        {"name": item["name"], "macAddress": item["macAddress"]}
        for item in agent_network_interfaces(agent, selected_macs)
    ]


def bmh_nmstate_interfaces(bmh, selected_macs):
    """Return NMState interface selectors from Metal3 hardware inventory.

    This is used before an Assisted Agent exists.  Metal3 exposes the same
    provider MAC-to-OS-name mapping in ``status.hardware.nics`` after host
    inspection, allowing the discovery NMStateConfig to be created before the
    Agent's first validation pass.
    """
    interfaces = bmh.get("status", {}).get("hardware", {}).get("nics", [])
    normalized = {str(mac).lower().replace("-", ":") for mac in selected_macs}
    result = []
    seen = set()
    for interface in interfaces:
        name = interface.get("name")
        mac = interface.get("mac") or interface.get("macAddress")
        if not name or not mac:
            continue
        key = (name, str(mac).lower().replace("-", ":"))
        if key in seen:
            continue
        seen.add(key)
        result.append({
            "name": name,
            "macAddress": mac,
            "selected": key[1] in normalized,
        })
    return result


def baremetal_instances_for_cluster(instances, cluster_id):
    """Select BMI objects owned by a ClusterOrder API ID.

    Older BMF objects used the cluster-order UUID label while newer objects
    carry the private Cluster API ID annotation. Accept both forms so the
    worker-network step is compatible across provider revisions.
    """
    selected = []
    for instance in instances or []:
        metadata = instance.get("metadata", {})
        labels = metadata.get("labels", {}) or {}
        annotations = metadata.get("annotations", {}) or {}
        if (
            labels.get("osac.openshift.io/clusterorder-uuid") == cluster_id
            or annotations.get("osac.openshift.io/cluster-api-id") == cluster_id
        ):
            selected.append(instance)
    return selected


class FilterModule:
    def filters(self):
        return {
            "mac_to_agent_name": mac_to_agent_name,
            "agent_vpc_interfaces": agent_vpc_interfaces,
            "agent_mgmt_ip": agent_mgmt_ip,
            "agent_network_interfaces": agent_network_interfaces,
            "agents_matching_selected_macs": agents_matching_selected_macs,
            "agent_nmstate_interfaces": agent_nmstate_interfaces,
            "bmh_nmstate_interfaces": bmh_nmstate_interfaces,
            "baremetal_instances_for_cluster": baremetal_instances_for_cluster,
        }
