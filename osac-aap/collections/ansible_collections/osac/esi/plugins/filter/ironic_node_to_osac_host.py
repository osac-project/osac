#!/usr/bin/python3

def ironic_node_to_osac_host(ironic_node):
    host = {
        "name": ironic_node["name"],
        "host_type": ironic_node["resource_class"],
        "power_state": ironic_node["power_state"],
        "target_power_state": ironic_node["target_power_state"],
    }
    return host


class FilterModule(object):
    def filters(self):
        return {
            'ironic_node_to_osac_host': ironic_node_to_osac_host
        }
