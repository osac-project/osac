#!/usr/bin/python
# -*- coding: utf-8 -*-

# (c) 2020, Simon Dodsley (simon@purestorage.com)
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

from __future__ import absolute_import, division, print_function

__metaclass__ = type


ANSIBLE_METADATA = {
    "metadata_version": "1.1",
    "status": ["preview"],
    "supported_by": "community",
}


DOCUMENTATION = """
---
module: purefb_bucket_replica
version_added: '1.0.0'
short_description:  Manage bucket replica links between Everpure FlashBlades
description:
    - This module manages bucket replica links between Everpure FlashBlades.
author: Everpure Ansible Team (@sdodsley) <pure-ansible-team@purestorage.com>
options:
  name:
    description:
      - Local Bucket Name.
    required: true
    type: str
  state:
    description:
      - Creates or modifies a bucket replica link
    required: false
    default: present
    type: str
    choices: [ "present", "absent" ]
  target:
    description:
      - Remote array or target name to create replica on.
    required: false
    type: str
  target_bucket:
    description:
      - Name of target bucket name
      - If not supplied, will default to I(name).
    type: str
    required: false
  paused:
    description:
      - State of the bucket replica link
    type: bool
    default: false
  credential:
    description:
      - Name of remote credential name to use.
    required: false
    type: str
  cascading:
    description:
      - Objects replicated to this bucket via a replica link from
        another array will also be replicated by this link to the
        remote bucket
    type: bool
    default: false
    version_added: "1.14.0"
  context:
    description:
    - Name of fleet member on which to perform the operation.
    - This requires the array receiving the request is a member of a fleet
      and the context name to be a member of the same fleet.
    type: str
    default: ""
    version_added: "1.22.0"
extends_documentation_fragment:
    - purestorage.flashblade.purestorage.fb
"""

EXAMPLES = """
- name: Create new bucket replica from foo to bar on arrayB
  purestorage.flashblade.purefb_bucket_replica:
    name: foo
    target: arrayB
    target_bucket: bar
    credential: cred_1
    state: present
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Pause exisitng bucket replica link
  purestorage.flashblade.purefb_bucket_replica:
    name: foo
    paused: true
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Delete bucket replica link foo
  purestorage.flashblade.purefb_bucket_replica:
    name: foo
    state: absent
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641
"""

RETURN = """
"""

CONTEXT_API_VERSION = "2.17"

HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import (
        BucketReplicaLink,
        ReferenceWritable,
        BucketReplicaLinkPost,
    )
except ImportError:
    HAS_PYPURECLIENT = False


from ansible.module_utils.basic import AnsibleModule
from ansible_collections.purestorage.flashblade.plugins.module_utils.purefb import (
    get_system,
    purefb_argument_spec,
)
from ansible_collections.purestorage.flashblade.plugins.module_utils.common import (
    get_error_message,
)


def get_local_bucket(module, blade):
    """Return Bucket or None"""
    api_version = list(blade.get_versions().items)
    if CONTEXT_API_VERSION in api_version and module.params["context"]:
        res = blade.get_buckets(
            context_names=[module.params["context"]],
            names=[module.params["name"]],
        )
    else:
        res = blade.get_buckets(names=[module.params["name"]])
    if res.status_code == 200:
        items = list(res.items)
        if items:
            return items[0]
    return None


def get_remote_cred(module, blade, target):
    """Return Remote Credential or None"""
    api_version = list(blade.get_versions().items)
    if CONTEXT_API_VERSION in api_version and module.params["context"]:
        res = blade.get_object_store_remote_credentials(
            names=[target + "/" + module.params["credential"]],
            context_names=[module.params["context"]],
        )
    else:
        res = blade.get_object_store_remote_credentials(
            names=[target + "/" + module.params["credential"]]
        )
    if res.status_code == 200:
        items = list(res.items)
        if items:
            return items[0]
    return None


def get_local_rl(module, blade):
    """Return Bucket Replica Link or None"""
    api_version = list(blade.get_versions().items)
    if CONTEXT_API_VERSION in api_version and module.params["context"]:
        res = blade.get_bucket_replica_links(
            local_bucket_names=[module.params["name"]],
            context_names=[module.params["context"]],
        )
    else:
        res = blade.get_bucket_replica_links(local_bucket_names=[module.params["name"]])
    if res.status_code == 200:
        items = list(res.items)
        if items:
            return items[0]
    return None


def get_connected(module, blade):
    api_version = list(blade.get_versions().items)
    if CONTEXT_API_VERSION in api_version and module.params["context"]:
        connected_blades = blade.get_array_connections(
            context_names=[module.params["context"]]
        )
    else:
        connected_blades = blade.get_array_connections()
    for item in list(connected_blades.items):
        if item.remote.name == module.params["target"] and item.status in [
            "connected",
            "connecting",
            "partially_connected",
        ]:
            return item.remote.name
    if CONTEXT_API_VERSION in api_version and module.params["context"]:
        connected_targets = blade.get_targets(context_names=[module.params["context"]])
    else:
        connected_targets = blade.get_targets()
    for target in list(connected_targets.items):
        if target.name == module.params["target"] and target.status in [
            "connected",
            "connecting",
            "partially_connected",
        ]:
            return target.name
    return None


def create_rl(module, blade, remote_cred):
    """Create Bucket Replica Link"""
    changed = True
    api_version = list(blade.get_versions().items)
    if not module.check_mode:
        if not module.params["target_bucket"]:
            module.params["target_bucket"] = module.params["name"]
        else:
            module.params["target_bucket"] = module.params["target_bucket"].lower()
        new_rl = BucketReplicaLinkPost(
            cascading_enabled=module.params["cascading"],
            paused=module.params["paused"],
        )
        if CONTEXT_API_VERSION in api_version and module.params["context"]:
            res = blade.post_bucket_replica_links(
                local_bucket_names=[module.params["name"]],
                remote_bucket_names=[module.params["target_bucket"]],
                remote_credentials_names=[remote_cred.name],
                bucket_replica_link=new_rl,
                context_names=[module.params["context"]],
            )
        else:
            res = blade.post_bucket_replica_links(
                local_bucket_names=[module.params["name"]],
                remote_bucket_names=[module.params["target_bucket"]],
                remote_credentials_names=[remote_cred.name],
                bucket_replica_link=new_rl,
            )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to create bucket replica link {0}.".format(
                    module.params["name"]
                )
            )
    module.exit_json(changed=changed)


def update_rl_policy(module, blade, local_replica_link):
    """Update Bucket Replica Link"""
    api_version = list(blade.get_versions().items)
    changed = False
    new_cred = local_replica_link.remote.name + "/" + module.params["credential"]
    if local_replica_link.paused != module.params["paused"]:
        paused = module.params["paused"]
        changed = True
    else:
        paused = local_replica_link.paused
    if local_replica_link.remote_credentials.name != new_cred:
        new_rl_cred = new_cred
        changed = True
    else:
        new_rl_cred = local_replica_link.remote_credentials.name
    if local_replica_link.cascading_enabled != module.params["cascading"]:
        cascading = module.params["cascading"]
        changed = True
    else:
        cascading = local_replica_link.cascading_enabled
    if not module.check_mode and changed:
        if CONTEXT_API_VERSION in api_version and module.params["context"]:
            res = blade.patch_bucket_replica_links(
                local_bucket_names=[module.params["name"]],
                remote_bucket_names=[local_replica_link.remote_bucket.name],
                remote_names=[local_replica_link.remote.name],
                bucket_replica_link=BucketReplicaLink(
                    paused=paused,
                    remote_credentials=ReferenceWritable(name=new_rl_cred),
                    cascading_enabled=cascading,
                ),
                context_names=[module.params["context"]],
            )
        else:
            res = blade.patch_bucket_replica_links(
                local_bucket_names=[module.params["name"]],
                remote_bucket_names=[local_replica_link.remote_bucket.name],
                remote_names=[local_replica_link.remote.name],
                bucket_replica_link=BucketReplicaLink(
                    paused=paused,
                    remote_credentials=ReferenceWritable(name=new_rl_cred),
                    cascading_enabled=cascading,
                ),
            )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to update bucket replica link {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def delete_rl_policy(module, blade, local_replica_link):
    """Delete Bucket Replica Link"""
    api_version = list(blade.get_versions().items)
    changed = True
    if not module.check_mode:
        if CONTEXT_API_VERSION in api_version and module.params["context"]:
            res = blade.delete_bucket_replica_links(
                remote_names=[local_replica_link.remote.name],
                local_bucket_names=[module.params["name"]],
                remote_bucket_names=[local_replica_link.remote_bucket.name],
                context_names=[module.params["context"]],
            )
        else:
            res = blade.delete_bucket_replica_links(
                remote_names=[local_replica_link.remote.name],
                local_bucket_names=[module.params["name"]],
                remote_bucket_names=[local_replica_link.remote_bucket.name],
            )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to delete bucket replica link {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            name=dict(type="str", required=True),
            target=dict(type="str"),
            target_bucket=dict(type="str"),
            paused=dict(type="bool", default=False),
            cascading=dict(type="bool", default=False),
            credential=dict(type="str"),
            state=dict(default="present", choices=["present", "absent"]),
            context=dict(type="str", default=""),
        )
    )

    module = AnsibleModule(argument_spec, supports_check_mode=True)

    if not HAS_PYPURECLIENT:
        module.fail_json(msg="py-pure-client sdk is required for this module")

    state = module.params["state"]
    module.params["name"] = module.params["name"].lower()
    blade = get_system(module)
    api_version = list(blade.get_versions().items)
    if CONTEXT_API_VERSION in api_version and not module.params["context"]:
        # If no context is provided set the context to the local array name
        fleet_res = blade.get_fleets()
        if fleet_res.status_code == 200 and list(fleet_res.items):
            module.params["context"] = list(blade.get_arrays().items)[0].name

    local_replica_link = get_local_rl(module, blade)

    # Removing a replica link only requires the link itself. The local and
    # remote buckets, the target connection and the remote credential may
    # already have been deleted, so none of these are required when deleting.
    if state == "absent":
        if local_replica_link:
            delete_rl_policy(module, blade, local_replica_link)
        module.exit_json(changed=False)

    local_bucket = get_local_bucket(module, blade)

    if not module.params["target"] and not local_replica_link:
        module.fail_json(
            msg="target parameter is required when creating a new replica link"
        )

    target = get_connected(module, blade) if module.params["target"] else None

    if module.params["target"] and not target:
        module.fail_json(
            msg="Selected target {0} is not connected.".format(module.params["target"])
        )

    if local_replica_link and not module.params["credential"]:
        module.params["credential"] = local_replica_link.remote_credentials.name.split(
            "/"
        )[1]
    remote_cred = get_remote_cred(module, blade, target)
    if not remote_cred:
        module.fail_json(
            msg="Selected remote credential {0} does not exist for target {1}.".format(
                module.params["credential"], module.params["target"]
            )
        )

    if not local_bucket:
        module.fail_json(
            msg="Selected local bucket {0} does not exist.".format(
                module.params["name"]
            )
        )

    if local_replica_link:
        if local_replica_link.status == "unhealthy":
            module.fail_json(msg="Replica Link unhealthy - please check target")

    if not local_replica_link:
        create_rl(module, blade, remote_cred)
    else:
        update_rl_policy(module, blade, local_replica_link)

    module.exit_json(changed=False)


if __name__ == "__main__":
    main()
