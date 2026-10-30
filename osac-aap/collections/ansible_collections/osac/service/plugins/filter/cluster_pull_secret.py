import base64
import json

from ansible.errors import AnsibleFilterError


def cluster_pull_secret(resources, order):
    try:
        if len(resources) != 1:
            raise ValueError
        secret = resources[0]
        metadata = secret["metadata"]
        owner_matches = any(
            owner.get("controller") is True
            and owner.get("uid") == order["metadata"]["uid"]
            and owner.get("kind") == "ClusterOrder"
            and owner.get("name") == order["metadata"]["name"]
            and owner.get("apiVersion") == "osac.openshift.io/v1alpha1"
            for owner in metadata.get("ownerReferences", [])
        )
        if (
            not order["metadata"].get("uid")
            or metadata.get("deletionTimestamp")
            or not owner_matches
            or metadata["namespace"] != order["metadata"]["namespace"]
            or metadata["name"] != order["spec"]["pullSecretRef"]["name"]
            or metadata.get("annotations", {}).get("osac.openshift.io/tenant", "")
            != order["metadata"].get("annotations", {}).get("osac.openshift.io/tenant", "")
            or secret["type"] != "kubernetes.io/dockerconfigjson"
        ):
            raise ValueError
        payload = base64.b64decode(secret["data"][".dockerconfigjson"], validate=True).decode()
        if not isinstance(json.loads(payload).get("auths"), dict):
            raise ValueError
        return payload
    except (KeyError, ValueError, TypeError, AttributeError):
        raise AnsibleFilterError("Referenced cluster pull secret is unavailable or invalid") from None


class FilterModule:
    def filters(self):
        return {"cluster_pull_secret": cluster_pull_secret}
