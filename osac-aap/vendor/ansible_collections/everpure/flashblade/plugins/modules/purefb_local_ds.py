#!/usr/bin/python
# -*- coding: utf-8 -*-

# (c) 2026, Simon Dodsley (simon@everpuredata.com)
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

from __future__ import absolute_import, division, print_function

__metaclass__ = type

ANSIBLE_METADATA = {
    "metadata_version": "1.1",
    "status": ["preview"],
    "supported_by": "community",
}

DOCUMENTATION = r"""
---
module: purefb_local_ds
version_added: '1.28.0'
short_description: Manage FlashBlade Local Directory Services
description:
- Create, update or delete a FlashBlade Local Directory Service (LDS).
- An LDS is the server-scoped container that owns local users and local
  groups (see M(everpure.flashblade.purefb_local_user) and
  M(everpure.flashblade.purefb_local_group)).
- Requires FlashBlade REST API version 2.24 or later (Purity//FB 4.6.8+).
- Server attachment is not managed here; attach an existing LDS to a server
  via M(everpure.flashblade.purefb_server). Rename and the destroyed /
  eradicate lifecycle are deferred until validated against a live 2.24 array.
author:
- Pure Storage Ansible Team (@avk) <pure-ansible-team@everpuredata.com>
options:
  name:
    description:
    - Name of the Local Directory Service.
    - Accepts the realm-qualified form C(realm::name) for realm-scoped LDSes.
    type: str
    required: true
  state:
    description:
    - Whether the LDS should exist or not.
    - I(state=absent) issues a direct DELETE. The module preflight-checks
      C(/servers) and refuses to delete an LDS still attached to any server;
      detach it via M(everpure.flashblade.purefb_server) first.
    type: str
    default: present
    choices: [ absent, present ]
  domain:
    description:
    - Domain string for the LDS.
    - Optional at create - when omitted, the array defaults the domain to
      the LDS name.
    - Patchable, but a domain change is disruptive; the API refuses it while
      the LDS is attached to a server. The module warns before issuing the
      PATCH in that case and surfaces the API error verbatim if rejected.
    - When omitted from the play on an existing LDS, the module does not
      touch the domain field (never "reset to LDS-name-default").
    type: str
  context:
    description:
    - Name of fleet member on which to perform the operation.
    - This requires the array receiving the request is a member of a fleet
      and the context name to be a member of the same fleet.
    type: str
    default: ""
extends_documentation_fragment:
- everpure.flashblade.everpure.fb
"""

EXAMPLES = r"""
- name: Create a local directory service with an explicit domain
  everpure.flashblade.purefb_local_ds:
    name: myserver_local
    domain: local
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Create a local directory service, letting the array default the domain
  everpure.flashblade.purefb_local_ds:
    name: myserver_local
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Change the domain on an existing LDS (must be detached from any server)
  everpure.flashblade.purefb_local_ds:
    name: myserver_local
    domain: corp.local
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Delete a local directory service (must be detached first)
  everpure.flashblade.purefb_local_ds:
    name: myserver_local
    state: absent
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Full workflow - create LDS, attach via purefb_server, add a user
  block:
    - name: Create LDS
      everpure.flashblade.purefb_local_ds:
        name: myserver_local
        domain: local
        fb_url: 10.10.10.2
        api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641
    - name: Attach LDS to server (see purefb_server for details)
      everpure.flashblade.purefb_server:
        name: myserver
        directory_service:
          - myserver_local
        fb_url: 10.10.10.2
        api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641
    - name: Create primary group for alice
      everpure.flashblade.purefb_local_group:
        fb_url: 10.10.10.2
        api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641
        name: alice
        local_directory_service: myserver_local
    - name: Create local user in the LDS
      everpure.flashblade.purefb_local_user:
        fb_url: 10.10.10.2
        api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641
        name: alice
        local_directory_service: myserver_local
        password: "s3cret!"
        primary_group: alice
"""

RETURN = r"""
"""


HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import (
        LocalDirectoryService,
        LocalDirectoryServicePost,
    )
except ImportError:
    HAS_PYPURECLIENT = False


from ansible.module_utils.basic import AnsibleModule
from ansible_collections.everpure.flashblade.plugins.module_utils.purefb import (
    get_system,
    purefb_argument_spec,
)
from ansible_collections.everpure.flashblade.plugins.module_utils.common import (
    get_error_message,
    get_rest_api_version,
)
from ansible_collections.everpure.flashblade.plugins.module_utils.version import (
    LooseVersion,
)

MIN_REQUIRED_API_VERSION = "2.24"


def _context_kwargs(module):
    """Return context_names kwargs when a context is set.

    LDS requires REST 2.24, which is well above the 2.17 fleet-context floor,
    so the array is guaranteed to accept context_names when we reach here.
    """
    if module.params["context"]:
        return {"context_names": [module.params["context"]]}
    return {}


def _get_lds(module, blade):
    ctx = _context_kwargs(module)
    res = blade.get_directory_services_local_directory_services(
        names=[module.params["name"]], **ctx
    )
    if res.status_code == 200:
        items = list(res.items)
        if items:
            return items[0]
    return None


def _find_attached_server(module, blade):
    """Return the name of a server attached to this LDS, or None.

    Fails the module if the attachment check itself errors, so the delete
    preflight can never silently pass on an API failure.
    """
    ctx = _context_kwargs(module)
    filter_expr = "local_directory_service.name='{0}'".format(module.params["name"])
    res = blade.get_servers(filter=filter_expr, **ctx)
    if res.status_code != 200:
        module.fail_json(
            msg=(
                "Failed to check server attachment for "
                "local directory service {0}. Error: {1}"
            ).format(module.params["name"], get_error_message(res))
        )
    items = list(res.items)
    if not items:
        return None
    return getattr(items[0], "name", None)


def delete_lds(module, blade):
    attached = _find_attached_server(module, blade)
    if attached:
        module.fail_json(
            msg=(
                "Local directory service '{0}' is attached to server '{1}'. "
                "Detach it via purefb_server before deleting."
            ).format(module.params["name"], attached)
        )
    changed = True
    if not module.check_mode:
        ctx = _context_kwargs(module)
        res = blade.delete_directory_services_local_directory_services(
            names=[module.params["name"]], **ctx
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to delete local directory service {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def create_lds(module, blade):
    changed = True
    if not module.check_mode:
        if module.params["domain"] is not None:
            body = LocalDirectoryServicePost(domain=module.params["domain"])
        else:
            body = LocalDirectoryServicePost()
        ctx = _context_kwargs(module)
        res = blade.post_directory_services_local_directory_services(
            names=[module.params["name"]], local_directory=body, **ctx
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to create local directory service {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def update_lds(module, blade, lds):
    ctx = _context_kwargs(module)
    patch_kwargs = {}
    if module.params["domain"] is not None and module.params["domain"] != getattr(
        lds, "domain", None
    ):
        patch_kwargs["domain"] = module.params["domain"]

    if not patch_kwargs:
        module.exit_json(changed=False)

    attached_server = getattr(lds, "server", None)
    attached_name = getattr(attached_server, "name", None) if attached_server else None
    if attached_name:
        module.warn(
            (
                "Changing domain on local directory service '{0}' is disruptive "
                "and is only permitted when detached; the array may reject this "
                "while '{0}' is attached to server '{1}'."
            ).format(module.params["name"], attached_name)
        )

    changed = True
    if not module.check_mode:
        res = blade.patch_directory_services_local_directory_services(
            names=[module.params["name"]],
            local_directory=LocalDirectoryService(**patch_kwargs),
            **ctx,
        )
        if res.status_code != 200:
            if attached_name:
                module.fail_json(
                    msg=(
                        "Failed to update local directory service {0} "
                        "(attached to server '{1}'). Error: {2}"
                    ).format(
                        module.params["name"], attached_name, get_error_message(res)
                    )
                )
            module.fail_json(
                msg="Failed to update local directory service {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            name=dict(type="str", required=True),
            state=dict(type="str", default="present", choices=["absent", "present"]),
            domain=dict(type="str"),
            context=dict(type="str", default=""),
        )
    )

    module = AnsibleModule(argument_spec, supports_check_mode=True)

    if not HAS_PYPURECLIENT:
        module.fail_json(msg="py-pure-client sdk is required for this module")

    blade = get_system(module)
    api_version = get_rest_api_version(blade)
    if LooseVersion(MIN_REQUIRED_API_VERSION) > LooseVersion(api_version):
        module.fail_json(
            msg=(
                "FlashBlade REST version not supported. "
                "Minimum version required: {0} (Purity//FB 4.6.8+)"
            ).format(MIN_REQUIRED_API_VERSION)
        )

    if not module.params["context"]:
        # If no context is provided set the context to the local array name
        fleet_res = blade.get_fleets()
        if fleet_res.status_code == 200 and list(fleet_res.items):
            module.params["context"] = list(blade.get_arrays().items)[0].name

    lds = _get_lds(module, blade)
    state = module.params["state"]

    if state == "absent" and lds:
        delete_lds(module, blade)
    elif state == "absent":
        module.exit_json(changed=False)
    elif state == "present" and not lds:
        create_lds(module, blade)
    else:
        update_lds(module, blade, lds)


if __name__ == "__main__":
    main()
