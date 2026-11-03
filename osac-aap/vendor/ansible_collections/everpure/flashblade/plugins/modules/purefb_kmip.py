#!/usr/bin/python
# -*- coding: utf-8 -*-

# (c) 2025, Simon Dodsley (simon@everpuredata.com)
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
module: purefb_kmip
version_added: '1.22.0'
short_description: Manage FlashBlade KMIP server objects
description:
- Manage FlashBlade KMIP Server objects
- A KMIP server object names one or more KMIP servers and the CA certificate,
  or certificate group, used to validate their authenticity.
author:
- Everpure Ansible Team (@sdodsley) <pure-ansible-team@everpuredata.com>
options:
  name:
    description:
    - Name of the KMIP server object
    type: str
    required: true
  state:
    description:
    - Action for the module to perform
    - I(test) reports the results of the array's KMIP connectivity tests and
      makes no changes
    default: present
    choices: [ absent, present, test ]
    type: str
  ca_certificate:
    type: str
    description:
    - Name of an existing certificate on the array, used to validate the
      authenticity of the configured KMIP servers.
    - Use the M(everpure.flashblade.purefb_certs) module to create certificates.
    - One of I(ca_certificate) or I(ca_certificate_group) is required when
      creating a new KMIP object.
  ca_certificate_group:
    type: str
    description:
    - Name of an existing certificate group on the array, containing CA
      certificates that can validate the authenticity of the configured
      KMIP servers.
    - The group must contain at least one certificate.
    - Use the M(everpure.flashblade.purefb_certgrp) module to create
      certificate groups.
    - One of I(ca_certificate) or I(ca_certificate_group) is required when
      creating a new KMIP object.
  uris:
    type: list
    elements: str
    description:
    - A list of URIs for the configured KMIP servers, in the format
      C([protocol://]hostname:port).
    - Required when creating a new KMIP server object.
extends_documentation_fragment:
- everpure.flashblade.everpure.fb
"""

EXAMPLES = r"""
- name: Create KMIP object
  everpure.flashblade.purefb_kmip:
    name: foo
    ca_certificate: kmip_ca_cert
    uris:
      - 1.1.1.1:8888
      - 2.3.3.3:9999
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6

- name: Update the servers in an existing KMIP object
  everpure.flashblade.purefb_kmip:
    name: foo
    uris:
      - 3.3.3.3:8888
      - 4.4.4.4:9999
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6

- name: Point a KMIP object at a certificate group instead
  everpure.flashblade.purefb_kmip:
    name: foo
    ca_certificate_group: kmip_ca_group
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6

- name: Test KMIP object connectivity
  everpure.flashblade.purefb_kmip:
    name: foo
    state: test
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6

- name: Delete KMIP object
  everpure.flashblade.purefb_kmip:
    name: foo
    state: absent
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
"""

RETURN = r"""
"""

HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import KmipServer, Reference
except ImportError:
    HAS_PYPURECLIENT = False

from ansible.module_utils.basic import AnsibleModule
from ansible_collections.everpure.flashblade.plugins.module_utils.purefb import (
    get_system,
    purefb_argument_spec,
)
from ansible_collections.everpure.flashblade.plugins.module_utils.common import (
    get_error_message,
)

# Module option -> KmipServer field. Both are references to an object that
# must already exist on the array, so the module sends Reference(name=...)
# and compares against the name on the current object.
REFERENCE_PARAMS = {
    "ca_certificate": "ca_certificate",
    "ca_certificate_group": "ca_certificate_group",
}


def _reference_name(obj, field):
    """Name held by a reference field on a KMIP object, or None."""
    return getattr(getattr(obj, field, None), "name", None)


def get_kmip(module, blade):
    """Return the named KMIP object, or None if it does not exist."""
    res = blade.get_kmip(names=[module.params["name"]])
    if res.status_code != 200:
        return None
    items = list(res.items)
    return items[0] if items else None


def _check_certificate(module, blade, param):
    """Fail unless the named certificate or certificate group exists."""
    name = module.params[param]
    if not name:
        return
    if param == "ca_certificate_group":
        res = blade.get_certificate_groups(names=[name])
        kind = "Certificate group"
    else:
        res = blade.get_certificates(names=[name])
        kind = "Certificate"
    if res.status_code != 200:
        module.fail_json(msg="{0} {1} does not exist.".format(kind, name))


def report_kmip_test(module, blade):
    """Report the array's KMIP connectivity tests. Makes no changes."""
    test_response = []
    response = list(blade.get_kmip_test(names=[module.params["name"]]).items)
    for component in response:
        test_response.append(
            {
                "component_address": component.component_address,
                "component_name": component.component_name,
                "description": component.description,
                "destination": component.destination,
                "enabled": bool(component.enabled),
                "result_details": getattr(component, "result_details", ""),
                "success": bool(component.success),
                "test_type": component.test_type,
                "resource_name": _reference_name(component, "resource"),
            }
        )
    module.exit_json(changed=False, test_response=test_response)


def update_kmip(module, blade, current_kmip):
    """PATCH only the settings that differ from the current object."""
    patch_kwargs = {}
    if module.params["uris"] is not None:
        current_uris = sorted(current_kmip.uris or [])
        wanted_uris = sorted(module.params["uris"])
        if current_uris != wanted_uris:
            patch_kwargs["uris"] = wanted_uris
    for param, field in REFERENCE_PARAMS.items():
        wanted = module.params[param]
        if wanted is None:
            continue
        if wanted != _reference_name(current_kmip, field):
            _check_certificate(module, blade, param)
            patch_kwargs[field] = Reference(name=wanted)

    changed = bool(patch_kwargs)
    if changed and not module.check_mode:
        res = blade.patch_kmip(
            names=[module.params["name"]],
            kmip_server=KmipServer(**patch_kwargs),
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Updating existing KMIP object {0} failed. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def create_kmip(module, blade):
    """Create a new KMIP object."""
    if not module.params["uris"]:
        module.fail_json(msg="uris is required to create a new KMIP object")
    if not (module.params["ca_certificate"] or module.params["ca_certificate_group"]):
        # The array rejects a KMIP object with neither: "Either a CA
        # certificate or a CA certificate group must be specified."
        module.fail_json(
            msg="One of ca_certificate or ca_certificate_group is required "
            "to create a new KMIP object"
        )
    post_kwargs = {"uris": sorted(module.params["uris"])}
    for param, field in REFERENCE_PARAMS.items():
        if module.params[param]:
            _check_certificate(module, blade, param)
            post_kwargs[field] = Reference(name=module.params[param])

    changed = True
    if not module.check_mode:
        res = blade.post_kmip(
            names=[module.params["name"]],
            kmip_server=KmipServer(**post_kwargs),
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Creating KMIP object {0} failed. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def delete_kmip(module, blade):
    """Delete an existing KMIP object."""
    changed = True
    if not module.check_mode:
        res = blade.delete_kmip(names=[module.params["name"]])
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to delete {0} KMIP object. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            state=dict(
                type="str",
                default="present",
                choices=["absent", "present", "test"],
            ),
            name=dict(type="str", required=True),
            ca_certificate=dict(type="str"),
            ca_certificate_group=dict(type="str"),
            uris=dict(type="list", elements="str"),
        )
    )

    module = AnsibleModule(
        argument_spec,
        supports_check_mode=True,
    )

    if not HAS_PYPURECLIENT:
        module.fail_json(msg="py-pure-client sdk is required for this module")

    blade = get_system(module)
    state = module.params["state"]
    current_kmip = get_kmip(module, blade)

    if state == "test":
        if not current_kmip:
            module.fail_json(
                msg="KMIP object {0} does not exist.".format(module.params["name"])
            )
        report_kmip_test(module, blade)
    elif state == "present" and not current_kmip:
        create_kmip(module, blade)
    elif state == "present" and current_kmip:
        update_kmip(module, blade, current_kmip)
    elif state == "absent" and current_kmip:
        delete_kmip(module, blade)

    module.exit_json(changed=False)


if __name__ == "__main__":
    main()
