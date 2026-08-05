import re

re_node_location = re.compile(
        "moc-r([0-9]+)p([a-z])c([0-9]+)u([0-9]+)(-s(.*))?")


def extract_esi_location(v: str) -> dict[str, str]:
    mo = re_node_location.match(v.lower())
    if not mo:
        return {}

    return {
        "cabinet": mo.group(3),
        "pod": mo.group(2),
        "row": mo.group(1),
        "slot": mo.group(6),
        "u": mo.group(4),
    }


def mac_to_agent_name(v: list[str], agents) -> str | None:
    try:
        from ansible_collections.osac.service.plugins.filter.agents \
                import mac_to_agent_name as osac_mac_to_agent_name

        return osac_mac_to_agent_name(v, agents)
    except ImportError:
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


def get_agent_metadata(node_info_list, agents):
    agent_metadata = []

    for node_info in node_info_list:
        node_addresses = [port.get('address')
                          for port in node_info.get('ports', [])
                          if port.get('address')]
        agent_name = mac_to_agent_name(node_addresses, agents)

        annotations = {"osac.openshift.io/host_uuid": node_info['id']}

        topology = extract_esi_location(node_info['name'])
        topology_labels = {"topology.nerc.mghpcc.org/%s" % label: str(val)
                           for label, val in topology.items()
                           if val is not None}
        resource_class_label = {
            'osac.openshift.io/resource_class':
                node_info.get('resource_class', '')
        }
        labels = {**topology_labels, **resource_class_label}

        agent_metadata.append({
            "name": agent_name,
            "hostname": node_info['name'],
            "annotations": annotations,
            "labels": labels,
        })

    return agent_metadata


class FilterModule:
    def filters(self):
        return {
            "extract_esi_location": extract_esi_location,
            "mac_to_agent_name": mac_to_agent_name,
            "get_agent_metadata": get_agent_metadata,
        }


def test_extract_esi_location():
    samples = {
        "MOC-R4PAC24U35-S3A": {
            "cabinet": "24",
            "pod": "A",
            "row": "4",
            "slot": "3A",
            "u": "35",
        },
        "MOC-R8PAC23U26": {
            "cabinet": "23",
            "pod": "A",
            "row": "8",
            "slot": None,
            "u": "26",
        },
    }

    for name, want in samples.items():
        try:
            have = extract_esi_location(name)
            assert have == want
        except AssertionError:
            print(f"have = {have}")
            print(f"want = {want}")
            raise
