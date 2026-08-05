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


class FilterModule:
    def filters(self):
        return {
            "mac_to_agent_name": mac_to_agent_name,
            "agent_vpc_interfaces": agent_vpc_interfaces,
            "agent_mgmt_ip": agent_mgmt_ip,
        }
