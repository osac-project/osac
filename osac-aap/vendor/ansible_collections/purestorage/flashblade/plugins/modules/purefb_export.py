#!/usr/bin/python
# -*- coding: utf-8 -*-

# (c) 2026, Simon Dodsley (simon@purestorage.com)
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
module: purefb_export
version_added: "1.25.0"
short_description:  Manage filesystem exports on Everpure FlashBlade`
description:
    - This module manages filesystem exports on Everpure FlashBlade.
author: Everpure Ansible Team (@sdodsley) <pure-ansible-team@purestorage.com>
options:
  name:
    description:
      - Export Name.
    required: true
    type: str
  server:
    description:
      - Name of server to assign export to
    type: str
    default: "_array_server"
  filesystem:
    description:
      - Filesystem to create the export for.
    required: true
    type: str
  state:
    description:
      - Create, delete or modifies a filesystem export.
    required: false
    default: present
    type: str
    choices: [ "present", "absent" ]
  type:
    description:
      - Type of filesystem export
    type: str
    choices: [ "NFS", "SMB" ]
    default: NFS
  export_policy:
    description:
    - Name of NFS export policy to assign to the export
    type: str
  share_policy:
    description:
    - Name of SMB share policy to assign to the export
    type: str
  client_policy:
    description:
    - Name of SMB client policy to assign to the export
    type: str
  rename:
    description:
      - New name for export
    type: str
  context:
    description:
    - Name of fleet member on which to perform the operation.
    - This requires the array receiving the request is a member of a fleet
      and the context name to be a member of the same fleet.
    type: str
    default: ""
extends_documentation_fragment:
    - purestorage.flashblade.purestorage.fb
"""

EXAMPLES = """
- name: Create new filesystem NFS export foo for filesystem bar
  purestorage.flashblade.purefb_export:
    name: foo
    filesystem: bar
    export_policy: acme_1
    state: present
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Create new filesystem NFS export foo for filesystem bar on server test
  purestorage.flashblade.purefb_export:
    name: foo
    server: test
    filesystem: bar
    export_policy: acme_1
    state: present
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Create new filesystem SMB export foo for filesystem bar
  purestorage.flashblade.purefb_export:
    name: foo
    filesystem: bar
    type: SMB
    state: present
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Delete filesystem export foo on server test
  purestorage.flashblade.purefb_export:
    name: foo
    server: test
    state: absent
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641
"""

RETURN = """
"""

HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import (
        FileSystemExport,
        FileSystemExportPost,
        Reference,
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

MIN_API_VERSION = "2.17"


def _context_kwargs(module):
    """Return context_names kwargs only when a context is set.

    A standalone array is "not in a fleet" and rejects fleet context, so
    only send context_names when a context has actually been provided.
    """
    if module.params["context"]:
        return {"context_names": [module.params["context"]]}
    return {}


def get_export(module, blade):
    """Return Filesystem export true name or None"""
    filter_string = (
        "export_name='"
        + module.params["name"]
        + "' and policy_type='"
        + module.params["type"]
        + "' and member.name='"
        + module.params["filesystem"]
        + "' and server.name='"
        + module.params["server"]
        + "'"
    )
    res = blade.get_file_system_exports(filter=filter_string, **_context_kwargs(module))
    if res.status_code == 200 and res.total_item_count != 0:
        return list(res.items)[0]
    return None


def create_export(module, blade):
    """Create Filesystem Export"""
    changed = True
    if not module.check_mode:
        if module.params["type"] == "NFS":
            exp_obj = FileSystemExportPost(
                export_name=module.params["name"],
                server=Reference(name=module.params["server"]),
            )
            res = blade.post_file_system_exports(
                file_system_export=exp_obj,
                member_names=[module.params["filesystem"]],
                policy_names=[module.params["export_policy"]],
                **_context_kwargs(module),
            )
        else:
            if not module.params["client_policy"]:
                module.params["client_policy"] = "_smb_client_allow_everyone"
            if not module.params["share_policy"]:
                module.params["share_policy"] = "_smb_share_allow_everyone"
            exp_obj = FileSystemExportPost(
                export_name=module.params["name"],
                server=Reference(name=module.params["server"]),
                share_policy=Reference(name=module.params["share_policy"]),
            )
            res = blade.post_file_system_exports(
                file_system_export=exp_obj,
                member_names=[module.params["filesystem"]],
                policy_names=[module.params["client_policy"]],
                **_context_kwargs(module),
            )

    if res.status_code != 200:
        module.fail_json(
            msg="Failed to create export {0} for {1}. Error: {2}".format(
                module.params["name"],
                module.params["filesystem"],
                get_error_message(res),
            )
        )
    module.exit_json(changed=changed)


def modify_export(module, blade, export):
    """Modify Filesystem"""
    changed_export = False
    changed_policy = False
    if module.params["rename"] and module.params["rename"] != module.params["name"]:
        export_name = module.params["rename"]
        changed_export = True
    else:
        export_name = module.params["name"]
    if module.params["type"] == "NFS":
        if (
            module.params["export_policy"]
            and module.params["export_policy"] != export.policy.name
        ):
            export_policy = module.params["export_policy"]
            changed_policy = True
        else:
            export_policy = export.policy.name
        if changed_policy or changed_export:
            if not module.check_mode:
                exp_obj = FileSystemExport()
                if changed_policy and not changed_export:
                    exp_obj = FileSystemExport(policy=Reference(name=export_policy))
                elif changed_policy and changed_export:
                    exp_obj = FileSystemExport(
                        export_name=export_name, policy=Reference(name=export_policy)
                    )
                elif not changed_policy and changed_export:
                    exp_obj = FileSystemExport(
                        export_name=export_name,
                    )
                res = blade.patch_file_system_exports(
                    names=[export.name], file_system_export=exp_obj
                )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to update export {0}. Error: {1}".format(
                            export.name, get_error_message(res)
                        )
                    )
    else:
        if (
            module.params["client_policy"]
            and module.params["client_policy"] != export.policy.name
        ):
            client_policy = module.params["client_policy"]
            changed_policy = True
        else:
            client_policy = export.policy.name
        if (
            module.params["share_policy"]
            and module.params["share_policy"] != export.share_policy.name
        ):
            share_policy = module.params["share_policy"]
            changed_policy = True
        else:
            share_policy = export.share_policy.name
        if changed_policy or changed_export:
            if not module.check_mode:
                exp_obj = FileSystemExport()
                if changed_policy and not changed_export:
                    exp_obj = FileSystemExport(
                        share_policy=Reference(name=share_policy),
                        policy=Reference(name=client_policy),
                    )
                elif changed_policy and changed_export:
                    exp_obj = FileSystemExport(
                        export_name=export_name,
                        share_policy=Reference(name=share_policy),
                        policy=Reference(name=client_policy),
                    )
                elif not changed_policy and changed_export:
                    exp_obj = FileSystemExport(
                        export_name=export_name,
                    )
                res = blade.patch_file_system_exports(
                    names=[export.name], file_system_export=exp_obj
                )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to update export {0}. Error: {1}".format(
                            export.name, get_error_message(res)
                        )
                    )

    module.exit_json(changed=(changed_policy or changed_export))


def delete_export(module, blade, export):
    """Delete Filesystem Export"""
    changed = True
    if not module.check_mode:
        res = blade.delete_file_system_exports(names=[export])
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to delete export {0}. Error: {1}".format(
                    export, get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            name=dict(type="str", required=True),
            server=dict(type="str", default="_array_server"),
            filesystem=dict(type="str", required=True),
            rename=dict(type="str"),
            type=dict(type="str", choices=["NFS", "SMB"], default="NFS"),
            state=dict(default="present", choices=["present", "absent"]),
            export_policy=dict(type="str"),
            share_policy=dict(type="str"),
            client_policy=dict(type="str"),
            context=dict(type="str", default=""),
        )
    )

    module = AnsibleModule(argument_spec, supports_check_mode=True)

    state = module.params["state"]
    blade = get_system(module)
    api_version = list(blade.get_versions().items)
    if MIN_API_VERSION in api_version and not module.params["context"]:
        # If no context is provided set the context to the local array name
        fleet_res = blade.get_fleets()
        if fleet_res.status_code == 200 and list(fleet_res.items):
            module.params["context"] = list(blade.get_arrays().items)[0].name
    server_exists = bool(
        blade.get_servers(names=[module.params["server"]]).status_code == 200
    )
    if not server_exists:
        module.fail_json(
            msg="Server {0} does not exist.".format(module.params["server"])
        )
    export = get_export(module, blade)

    if state == "present" and not export and not module.params["rename"]:
        create_export(module, blade)
    elif state == "present" and export:
        modify_export(module, blade, export)
    elif state == "absent" and export:
        delete_export(module, blade, export.name)
    elif state == "absent" and not export:
        module.exit_json(changed=False)
    module.exit_json(changed=False)


if __name__ == "__main__":
    main()
