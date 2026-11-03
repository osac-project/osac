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
module: purefb_tls_policy
version_added: '1.28.0'
short_description: Manage FlashBlade TLS policies
description:
- Create, update or delete FlashBlade TLS policies and manage the
  network interfaces they are applied to.
- A TLS policy controls the server certificate presented on a network
  interface, the TLS versions and ciphers permitted, and optional
  mutual-TLS settings for verifying client certificates.
author:
- Pure Storage Ansible Team (@avk) <pure-ansible-team@everpuredata.com>
options:
  name:
    description:
    - Name of the TLS policy.
    type: str
    required: true
  state:
    description:
    - Define whether the TLS policy should exist or not.
    default: present
    choices: [ absent, present ]
    type: str
  enabled:
    description:
    - If C(true) the policy is enabled.
    - Defaults to C(true) on creation if not specified.
    type: bool
  appliance_certificate:
    description:
    - Name of the certificate to present as the server certificate during
      TLS negotiation with clients connecting to network interfaces
      covered by this policy.
    - Required when creating a policy; the array rejects a policy without
      an appliance certificate. Use C(global) for the built-in certificate.
    - Set to C(global) to reset the policy to the built-in appliance
      certificate, effectively removing a previously assigned custom
      certificate.
    type: str
  client_certificates_required:
    description:
    - If C(true), all clients must present a client certificate during
      TLS negotiation; failure to do so will be rejected.
    - If C(false), client certificates are optional.
    - The array only accepts C(true) when
      I(verify_client_certificate_trust) is also C(true), which in turn
      needs I(trusted_client_certificate_authority).
    - Requires Purity//FB REST API 2.18 or later.
    type: bool
  trusted_client_certificate_authority:
    description:
    - Name of a certificate, or certificate group, used to verify
      certificates presented by clients when
      I(verify_client_certificate_trust) is C(true).
    - Set to an empty string (C("")) to clear a previously assigned
      trusted CA reference. This only applies on update; on create an
      empty string is treated the same as omitting the parameter.
    - Requires Purity//FB REST API 2.18 or later.
    type: str
  verify_client_certificate_trust:
    description:
    - If C(true), certificates presented by clients in TLS negotiation
      will undergo strict trust verification using the certificate(s)
      referenced by I(trusted_client_certificate_authority).
    - Requires Purity//FB REST API 2.18 or later.
    type: bool
  min_tls_version:
    description:
    - Minimum TLS version permitted for inbound connections.
    - C(default) lets the array pick a recommended minimum that may shift
      across software upgrades.
    type: str
    choices: [ default, '1.0', '1.1', '1.2', '1.3' ]
  enabled_tls_ciphers:
    description:
    - List of TLS ciphers to enable.
    - When supplied, only these ciphers will be enabled.
    type: list
    elements: str
  disabled_tls_ciphers:
    description:
    - List of TLS ciphers to disable.
    type: list
    elements: str
  network_interfaces:
    description:
    - Desired set of network interfaces this policy should be applied to.
    - On update, the set of attached interfaces is reconciled against
      this list - missing interfaces are attached, extras are detached.
    - Omit entirely to leave existing attachments untouched.
    - Use an empty list to detach the policy from all interfaces.
    - On create, an empty list creates the policy with no interfaces
      attached; this is equivalent to omitting the parameter.
    type: list
    elements: str
extends_documentation_fragment:
- everpure.flashblade.everpure.fb
"""

EXAMPLES = r"""
- name: Create TLS policy with a server cert and minimum TLS 1.2
  everpure.flashblade.purefb_tls_policy:
    name: secure_data_plane
    appliance_certificate: my_cert
    min_tls_version: '1.2'
    fb_url: 10.10.10.2
    api_token: T-68618f31-0c9e-4e57-aa44-5306a2cf10e3

- name: Require mutual TLS using a trusted CA group
  everpure.flashblade.purefb_tls_policy:
    name: secure_data_plane
    client_certificates_required: true
    verify_client_certificate_trust: true
    trusted_client_certificate_authority: corp_ca_group
    fb_url: 10.10.10.2
    api_token: T-68618f31-0c9e-4e57-aa44-5306a2cf10e3

- name: Apply the policy to a specific set of data network interfaces
  everpure.flashblade.purefb_tls_policy:
    name: secure_data_plane
    network_interfaces:
      - data1.eth0
      - data2.eth0
    fb_url: 10.10.10.2
    api_token: T-68618f31-0c9e-4e57-aa44-5306a2cf10e3

- name: Detach the TLS policy from all network interfaces
  everpure.flashblade.purefb_tls_policy:
    name: secure_data_plane
    network_interfaces: []
    fb_url: 10.10.10.2
    api_token: T-68618f31-0c9e-4e57-aa44-5306a2cf10e3

- name: Disable a TLS policy without deleting it
  everpure.flashblade.purefb_tls_policy:
    name: secure_data_plane
    enabled: false
    fb_url: 10.10.10.2
    api_token: T-68618f31-0c9e-4e57-aa44-5306a2cf10e3

- name: Reset appliance cert to the built-in global cert and clear the trusted CA
  everpure.flashblade.purefb_tls_policy:
    name: secure_data_plane
    appliance_certificate: global
    trusted_client_certificate_authority: ""
    fb_url: 10.10.10.2
    api_token: T-68618f31-0c9e-4e57-aa44-5306a2cf10e3

- name: Delete a TLS policy
  everpure.flashblade.purefb_tls_policy:
    name: secure_data_plane
    state: absent
    fb_url: 10.10.10.2
    api_token: T-68618f31-0c9e-4e57-aa44-5306a2cf10e3
"""

RETURN = r"""
"""

HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import (
        TlsPolicy,
        TlsPolicyPost,
        ReferenceWritable,
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

MIN_REQUIRED_API_VERSION = "2.17"
MIN_MUTUAL_TLS_API_VERSION = "2.18"
MUTUAL_TLS_PARAMS = (
    "client_certificates_required",
    "trusted_client_certificate_authority",
    "verify_client_certificate_trust",
)


def _check_mutual_tls_support(module, api_version):
    """Fail early if mutual-TLS options were set but the array does not advertise REST 2.18.

    pypureclient silently drops unknown fields when serialising TlsPolicyPost, so
    without this guard a user on 2.17 would get a successful create with the
    mutual-TLS settings missing from the policy.
    """
    if LooseVersion(MIN_MUTUAL_TLS_API_VERSION) > LooseVersion(api_version):
        used = [p for p in MUTUAL_TLS_PARAMS if module.params.get(p) is not None]
        if used:
            module.fail_json(
                msg=(
                    "Mutual TLS options ({0}) require Purity//FB REST API {1} or later."
                ).format(", ".join(used), MIN_MUTUAL_TLS_API_VERSION)
            )


def _validate_attachable(module, blade, interfaces):
    """Validate every interface in C(interfaces) exists and isn't bound to another TLS policy.

    Called for the to-attach subset only: interfaces already bound to *this* policy
    do not conflict and are left alone.
    """
    if not interfaces:
        return
    res = blade.get_network_interfaces(
        names=interfaces,
    )
    if res.status_code != 200:
        module.fail_json(
            msg="Network interface(s) not found: {0}. Error: {1}".format(
                interfaces, get_error_message(res)
            )
        )
    found = {getattr(i, "name", None) for i in (res.items or [])}
    missing = [i for i in interfaces if i not in found]
    if missing:
        module.fail_json(msg="Network interface(s) not found: {0}".format(missing))

    res = blade.get_tls_policies_network_interfaces(
        member_names=interfaces,
    )
    if res.status_code != 200:
        return
    our_name = module.params["name"]
    conflicts = []
    for item in res.items or []:
        policy_name = getattr(getattr(item, "policy", None), "name", None)
        member_name = getattr(getattr(item, "member", None), "name", None)
        if policy_name and policy_name != our_name:
            conflicts.append((member_name, policy_name))
    if conflicts:
        formatted = ", ".join("{0}=>{1}".format(m, p) for m, p in conflicts)
        module.fail_json(
            msg=(
                "Cannot attach TLS policy {0} - the following interface(s) already "
                "have a different TLS policy applied: {1}. Detach the existing "
                "policy before re-attaching."
            ).format(our_name, formatted)
        )


def _get_policy(module, blade):
    """Return the existing TLS policy object or None."""
    res = blade.get_tls_policies(
        names=[module.params["name"]],
    )
    if res.status_code != 200:
        return None
    items = list(res.items)
    return items[0] if items else None


def _get_attached_interfaces(module, blade):
    """Return a sorted list of network interface names this policy is applied to."""
    res = blade.get_tls_policies_network_interfaces(
        policy_names=[module.params["name"]],
    )
    if res.status_code != 200:
        return []
    names = []
    for item in res.items or []:
        member = getattr(item, "member", None)
        member_name = getattr(member, "name", None)
        if member_name:
            names.append(member_name)
    return sorted(names)


def _build_post_kwargs(module):
    """Build kwargs for TlsPolicyPost from module params."""
    kwargs = {}
    if module.params["enabled"] is not None:
        kwargs["enabled"] = module.params["enabled"]
    if module.params["appliance_certificate"]:
        kwargs["appliance_certificate"] = ReferenceWritable(
            name=module.params["appliance_certificate"]
        )
    if module.params["client_certificates_required"] is not None:
        kwargs["client_certificates_required"] = module.params[
            "client_certificates_required"
        ]
    if module.params["trusted_client_certificate_authority"]:
        kwargs["trusted_client_certificate_authority"] = ReferenceWritable(
            name=module.params["trusted_client_certificate_authority"]
        )
    if module.params["verify_client_certificate_trust"] is not None:
        kwargs["verify_client_certificate_trust"] = module.params[
            "verify_client_certificate_trust"
        ]
    if module.params["min_tls_version"]:
        kwargs["min_tls_version"] = module.params["min_tls_version"]
    if module.params["enabled_tls_ciphers"] is not None:
        kwargs["enabled_tls_ciphers"] = module.params["enabled_tls_ciphers"]
    if module.params["disabled_tls_ciphers"] is not None:
        kwargs["disabled_tls_ciphers"] = module.params["disabled_tls_ciphers"]
    return kwargs


def _reconcile_interfaces(module, blade):
    """Reconcile policy network-interface attachments to the desired set.

    Returns True if any attach/detach was needed.
    """
    desired = sorted(module.params["network_interfaces"])
    current = _get_attached_interfaces(module, blade)
    to_attach = [i for i in desired if i not in current]
    to_detach = [i for i in current if i not in desired]

    if not (to_attach or to_detach):
        return False

    if to_attach:
        _validate_attachable(module, blade, to_attach)

    if module.check_mode:
        return True

    attached = []
    for iface in to_attach:
        res = blade.post_tls_policies_network_interfaces(
            policy_names=[module.params["name"]],
            member_names=[iface],
        )
        if res.status_code != 200:
            note = " Already attached: {0}.".format(attached) if attached else ""
            module.fail_json(
                msg="Failed to attach TLS policy {0} to interface {1}. Error: {2}.{3}".format(
                    module.params["name"], iface, get_error_message(res), note
                )
            )
        attached.append(iface)

    detached = []
    for iface in to_detach:
        res = blade.delete_tls_policies_network_interfaces(
            policy_names=[module.params["name"]],
            member_names=[iface],
        )
        if res.status_code != 200:
            note = " Already detached: {0}.".format(detached) if detached else ""
            module.fail_json(
                msg="Failed to detach TLS policy {0} from interface {1}. Error: {2}.{3}".format(
                    module.params["name"], iface, get_error_message(res), note
                )
            )
        detached.append(iface)

    return True


def delete_policy(module, blade):
    """Delete a TLS policy, detaching it from any network interfaces first."""
    changed = True
    if not module.check_mode:
        attached = _get_attached_interfaces(module, blade)
        detached = []
        for iface in attached:
            res = blade.delete_tls_policies_network_interfaces(
                policy_names=[module.params["name"]],
                member_names=[iface],
            )
            if res.status_code != 200:
                note = " Already detached: {0}.".format(detached) if detached else ""
                module.fail_json(
                    msg="Failed to detach TLS policy {0} from interface {1}. Error: {2}.{3}".format(
                        module.params["name"], iface, get_error_message(res), note
                    )
                )
            detached.append(iface)
        res = blade.delete_tls_policies(
            names=[module.params["name"]],
        )
        if res.status_code != 200:
            detached_note = ""
            if attached:
                detached_note = (
                    " The policy has already been detached from interfaces {0};"
                    " re-run to retry deletion or reattach manually if needed."
                ).format(attached)
            module.fail_json(
                msg="Failed to delete TLS policy {0}. Error: {1}.{2}".format(
                    module.params["name"], get_error_message(res), detached_note
                )
            )
    module.exit_json(changed=changed)


def create_policy(module, blade):
    """Create a new TLS policy and optionally attach it to interfaces."""
    changed = True
    if not module.params["appliance_certificate"]:
        module.fail_json(
            msg=(
                "appliance_certificate is required to create TLS policy {0} "
                "(use 'global' for the built-in appliance certificate)."
            ).format(module.params["name"])
        )
    if module.params["network_interfaces"]:
        _validate_attachable(module, blade, module.params["network_interfaces"])
    if not module.check_mode:
        res = blade.post_tls_policies(
            names=[module.params["name"]],
            policy=TlsPolicyPost(**_build_post_kwargs(module)),
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to create TLS policy {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
        attached = []
        for iface in module.params["network_interfaces"] or []:
            res = blade.post_tls_policies_network_interfaces(
                policy_names=[module.params["name"]],
                member_names=[iface],
            )
            if res.status_code != 200:
                note = " Already attached: {0}.".format(attached) if attached else ""
                module.fail_json(
                    msg="Failed to attach TLS policy {0} to interface {1}. Error: {2}.{3}".format(
                        module.params["name"], iface, get_error_message(res), note
                    )
                )
            attached.append(iface)
    module.exit_json(changed=changed)


def update_policy(module, blade, policy):
    """Update a TLS policy and reconcile its network-interface attachments."""
    changed = False
    patch_kwargs = {}

    if module.params["enabled"] is not None and module.params["enabled"] != getattr(
        policy, "enabled", None
    ):
        patch_kwargs["enabled"] = module.params["enabled"]

    if module.params["min_tls_version"] and module.params["min_tls_version"] != getattr(
        policy, "min_tls_version", None
    ):
        patch_kwargs["min_tls_version"] = module.params["min_tls_version"]

    if module.params["client_certificates_required"] is not None and module.params[
        "client_certificates_required"
    ] != getattr(policy, "client_certificates_required", None):
        patch_kwargs["client_certificates_required"] = module.params[
            "client_certificates_required"
        ]

    if module.params["verify_client_certificate_trust"] is not None and module.params[
        "verify_client_certificate_trust"
    ] != getattr(policy, "verify_client_certificate_trust", None):
        patch_kwargs["verify_client_certificate_trust"] = module.params[
            "verify_client_certificate_trust"
        ]

    if module.params["enabled_tls_ciphers"] is not None:
        current = list(getattr(policy, "enabled_tls_ciphers", None) or [])
        if sorted(module.params["enabled_tls_ciphers"]) != sorted(current):
            patch_kwargs["enabled_tls_ciphers"] = module.params["enabled_tls_ciphers"]

    if module.params["disabled_tls_ciphers"] is not None:
        current = list(getattr(policy, "disabled_tls_ciphers", None) or [])
        if sorted(module.params["disabled_tls_ciphers"]) != sorted(current):
            patch_kwargs["disabled_tls_ciphers"] = module.params["disabled_tls_ciphers"]

    if module.params["appliance_certificate"]:
        current = getattr(getattr(policy, "appliance_certificate", None), "name", None)
        if module.params["appliance_certificate"] != current:
            patch_kwargs["appliance_certificate"] = ReferenceWritable(
                name=module.params["appliance_certificate"]
            )

    if module.params["trusted_client_certificate_authority"] is not None:
        current = (
            getattr(
                getattr(policy, "trusted_client_certificate_authority", None),
                "name",
                None,
            )
            or ""
        )
        if module.params["trusted_client_certificate_authority"] != current:
            patch_kwargs["trusted_client_certificate_authority"] = ReferenceWritable(
                name=module.params["trusted_client_certificate_authority"]
            )

    if module.params["network_interfaces"] is not None:
        if _reconcile_interfaces(module, blade):
            changed = True

    if patch_kwargs:
        changed = True
        if not module.check_mode:
            res = blade.patch_tls_policies(
                names=[module.params["name"]],
                policy=TlsPolicy(**patch_kwargs),
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to update TLS policy {0}. Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )

    module.exit_json(changed=changed)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            state=dict(type="str", default="present", choices=["absent", "present"]),
            name=dict(type="str", required=True),
            enabled=dict(type="bool"),
            appliance_certificate=dict(type="str"),
            client_certificates_required=dict(type="bool"),
            trusted_client_certificate_authority=dict(type="str"),
            verify_client_certificate_trust=dict(type="bool"),
            min_tls_version=dict(
                type="str",
                choices=[
                    "default",
                    "1.0",
                    "1.1",
                    "1.2",
                    "1.3",
                ],
            ),
            enabled_tls_ciphers=dict(type="list", elements="str"),
            disabled_tls_ciphers=dict(type="list", elements="str"),
            network_interfaces=dict(type="list", elements="str"),
        )
    )

    module = AnsibleModule(
        argument_spec,
        required_if=[
            (
                "verify_client_certificate_trust",
                True,
                ["trusted_client_certificate_authority"],
            ),
        ],
        supports_check_mode=True,
    )

    if not HAS_PYPURECLIENT:
        module.fail_json(msg="py-pure-client sdk is required for this module")

    blade = get_system(module)
    api_version = get_rest_api_version(blade)
    if LooseVersion(MIN_REQUIRED_API_VERSION) > LooseVersion(api_version):
        module.fail_json(
            msg="FlashBlade REST version not supported. "
            "Minimum version required: {0}".format(MIN_REQUIRED_API_VERSION)
        )
    _check_mutual_tls_support(module, api_version)

    state = module.params["state"]
    policy = _get_policy(module, blade)

    if state == "present" and policy is None:
        create_policy(module, blade)
    elif state == "present" and policy is not None:
        update_policy(module, blade, policy)
    elif state == "absent" and policy is not None:
        delete_policy(module, blade)

    module.exit_json(changed=False)


if __name__ == "__main__":
    main()
