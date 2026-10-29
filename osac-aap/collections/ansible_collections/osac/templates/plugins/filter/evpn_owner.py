from ansible.errors import AnsibleFilterError


PREFIX = "osac.openshift.io/"
SUPPORTED = PREFIX + "compute-instance-supported"
REASON = PREFIX + "compute-instance-unsupported-reason"
OWNER = PREFIX + "cudn-owner-subnet"
CLEANED = PREFIX + "cudn-owner-cleaned"
LIMIT = "cudn_evpn supports VMs only on its VirtualNetwork's owner Subnet"


def annotations(resource):
    return resource.get("metadata", {}).get("annotations", {}) or {}


def evpn_persisted_owner(siblings, lease, cudn):
    owner = annotations(lease).get(OWNER, "")
    network_owner = annotations(cudn).get(OWNER, "")
    if owner and network_owner and owner != network_owner:
        raise AnsibleFilterError("Shared Lease and CUDN owners conflict; preserve networking and repair ownership")
    owner = owner or network_owner
    if not owner and cudn:
        historical = [
            name for name, item in ((item["metadata"]["name"], item) for item in siblings)
            if annotations(item).get(PREFIX + "k8s-implementation-strategy") == "cudn_evpn"
            and annotations(item).get(SUPPORTED) != "false"
            and item.get("spec", {}).get("ipv4Cidr") in
            cudn.get("spec", {}).get("network", {}).get("layer2", {}).get("subnets", [])
        ]
        if len(historical) != 1:
            raise AnsibleFilterError("Existing CUDN has no unambiguous owner; preserve networking and migrate ownership")
        owner = historical[0]
    return owner


def evpn_owner_decision(subnet, siblings, lease, cudn):
    current_name = subnet["metadata"]["name"]
    current_vnet = subnet["spec"]["virtualNetwork"]
    candidates = {
        item["metadata"]["name"]: item
        for item in siblings
        if item.get("spec", {}).get("virtualNetwork") == current_vnet
    }
    if current_name not in candidates:
        raise AnsibleFilterError("Subnet disappeared during EVPN owner classification; retry")
    subnet = candidates[current_name]
    owner = evpn_persisted_owner(list(candidates.values()), lease, cudn)
    owner_resource = candidates.get(owner)
    if owner and owner_resource is None:
        if annotations(lease).get(CLEANED) == "true" and not cudn:
            owner = ""
        else:
            return {"owner": owner, "retry": True, "supported": None, "reason": "Waiting for EVPN owner cleanup"}
    if annotations(subnet).get(PREFIX + "skip-k8s-manager") == "true":
        return {"owner": owner, "retry": False, "supported": False, "reason": "Subnet explicitly opts out of Kubernetes networking"}
    if annotations(subnet).get(SUPPORTED) == "false":
        return {"owner": owner, "retry": False, "supported": False, "reason": annotations(subnet).get(REASON) or LIMIT}
    if owner_resource and owner_resource.get("metadata", {}).get("deletionTimestamp"):
        return {"owner": owner, "retry": True, "supported": None, "reason": "Waiting for deleting EVPN owner Subnet"}
    if subnet.get("metadata", {}).get("deletionTimestamp"):
        return {"owner": owner, "retry": True, "supported": None, "reason": "Subnet is being deleted"}
    if not owner:
        owner = current_name
    return {"owner": owner, "retry": False, "supported": owner == current_name,
            "reason": "" if owner == current_name else LIMIT}


class FilterModule:
    def filters(self):
        return {"evpn_owner_decision": evpn_owner_decision, "evpn_persisted_owner": evpn_persisted_owner}
