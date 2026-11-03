#!/usr/bin/python
# -*- coding: utf-8 -*-

# (c) 2018, Simon Dodsley (simon@everpuredata.com)
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
module: purefb_ds
version_added: '1.0.0'
short_description: Configure FlashBlade Directory Service
description:
- Create, modify or erase directory services configurations. There is no
  facility to SSL certificates at this time. Use the FlashBlade GUI for this
  additional configuration work.
- If updating a directory service and I(bind_password) is provided this
  will always cause a change, even if the password given isn't different from
  the current, unless I(force_bind_password) is C(false). The array never
  reports the password, so the module cannot compare it.
author:
- Everpure Ansible Team (@sdodsley) <pure-ansible-team@everpuredata.com>
options:
  state:
    description:
    - Create, delete or test directory service configuration
    default: present
    type: str
    choices: [ absent, present, test ]
  dstype:
    description:
    - The type of directory service to work on
    choices: [ management, nfs, smb ]
    type: str
    required: true
  enable:
    description:
    - Whether to enable or disable directory service support.
    - If not specified an existing service keeps its current state and a
      new service is created disabled.
    type: bool
  uri:
    description:
    - A list of up to 30 URIs of the directory servers. Each URI must include
      the scheme ldap:// or ldaps:// (for LDAP over SSL), a hostname, and a
      domain name or IP address. For example, ldap://ad.company.com configures
      the directory service with the hostname "ad" in the domain "company.com"
      while specifying the unencrypted LDAP protocol.
    type: list
    elements: str
  base_dn:
    description:
    - Sets the base of the Distinguished Name (DN) of the directory service
      groups. The base should consist of only Domain Components (DCs). The
      base_dn will populate with a default value when a URI is entered by
      parsing domain components from the URI. The base DN should specify DC=
      for each domain component and multiple DCs should be separated by commas.
    type: str
  bind_password:
    description:
    - Sets the password of the bind_user user name account.
    - Required when I(uri) or I(bind_user) change an existing service.
    type: str
  force_bind_password:
    type: bool
    default: true
    description:
    - Will force the bind password to be reset even if the bind user password
      is unchanged.
    - If set to I(false) and I(bind_user) is unchanged the password will not
      be reset.
    - Has no effect when I(bind_password) is not provided.
    version_added: 1.16.0
  bind_user:
    description:
    - Sets the user name that can be used to bind to and query the directory.
    - For Active Directory, enter the username - often referred to as
      sAMAccountName or User Logon Name - of the account that is used to
      perform directory lookups.
    - For OpenLDAP, enter the full DN of the user.
    type: str
  nis_servers:
    description:
    - A list of up to 30 IP addresses or FQDNs for NIS servers.
    - This cannot be used in conjunction with LDAP configurations.
    type: list
    elements: str
  nis_domain:
    description:
    - The NIS domain to search
    - This cannot be used in conjunction with LDAP configurations.
    type: str
  join_ou:
    description:
    - The optional organizational unit (OU) where the machine account
      for the directory service will be created.
    type: str
  nfs_server:
    description:
     - Name of the internal server
     - Only required if I(nfs) is the I(dstype)
    type: str
    version_added: 1.23.0
extends_documentation_fragment:
- everpure.flashblade.everpure.fb
"""

EXAMPLES = r"""
- name: Delete existing management directory service
  everpure.flashblade.purefb_ds:
    dstype: management
    state: absent
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Create NFS directory service (disabled)
  everpure.flashblade.purefb_ds:
    dstype: nfs
    uri: "ldaps://lab.everpure.com"
    base_dn: "DC=lab,DC=everpure,DC=com"
    bind_user: Administrator
    bind_password: password
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Enable existing SMB directory service
  everpure.flashblade.purefb_ds:
    dstypr: smb
    enable: true
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Disable existing management directory service
  everpure.flashblade.purefb_ds:
    dstype: management
    enable: false
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Create NFS directory service (LDAP-based) - default internal server
  everpure.flashblade.purefb_ds:
    dstype: nfs
    enable: true
    uri: "ldaps://lab.everpure.com"
    base_dn: "DC=lab,DC=everpure,DC=com"
    bind_user: Administrator
    bind_password: password
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Create NFS directory service (NIS based) - alternate internal server
  everpure.flashblade.purefb_ds:
    dstype: nfs
    nfs_server: acme1
    enable: true
    nis_domain: "acme.com"
    nis_servers:
      - "1.1.1.1"
      - "2.2.2.2"
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Test array management directory service
  everpure.flashblade.purefb_ds:
    dstype: management
    state: test
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Test default nfs directory service
  everpure.flashblade.purefb_ds:
    dstype: nfs
    state: test
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Test NFS directory service for internal server fred
  everpure.flashblade.purefb_ds:
    dstype: nfs
    nfs_server: fred
    state: test
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641
"""

RETURN = r"""
"""


NO_SMB_VERSION = "2.16"

HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import DirectoryService
except ImportError:
    HAS_PYPURECLIENT = False


from ansible.module_utils.basic import AnsibleModule
from ansible_collections.everpure.flashblade.plugins.module_utils.purefb import (
    get_system,
    purefb_argument_spec,
)
from ansible_collections.everpure.flashblade.plugins.module_utils.version import (
    LooseVersion,
)
from ansible_collections.everpure.flashblade.plugins.module_utils.common import (
    get_error_message,
    get_rest_api_version,
)


def delete_ds(module, blade):
    """Delete Directory Service"""
    changed = True
    ds_name = module.params["dstype"]
    if not module.check_mode:
        if module.params["nfs_server"] and module.params["dstype"] == "nfs":
            if module.params["nfs_server"] != "_array_server":
                ds_name = module.params["nfs_server"] + "_nfs"
        res = blade.get_directory_services(names=[ds_name])
        if res.status_code != 200:
            module.fail_json(
                msg="Fetch {0} Directory Service failed. Error: {1}".format(
                    module.params["dstype"], get_error_message(res)
                )
            )
        dirserv = list(res.items)[0]
        dir_service = DirectoryService()  # Initialize for pylint
        if module.params["dstype"] == "management":
            if dirserv.uris:
                dir_service = DirectoryService(
                    uris=[""],
                    base_dn="",
                    bind_user="",
                    bind_password="",
                    enabled=False,
                )
            else:
                changed = False
        elif module.params["dstype"] == "smb":
            if dirserv.uris:
                smb_attrs = {"join_ou": ""}
                dir_service = DirectoryService(
                    uris=[""],
                    base_dn="",
                    bind_user="",
                    bind_password="",
                    smb=smb_attrs,
                    enabled=False,
                )
            else:
                changed = False
        elif module.params["dstype"] == "nfs":
            if dirserv.uris:
                dir_service = DirectoryService(
                    uris=[""],
                    base_dn="",
                    bind_user="",
                    bind_password="",
                    enabled=False,
                )
            elif getattr(dirserv.nfs, "nis_domains", None):
                nfs_attrs = {"nis_domains": [], "nis_servers": []}
                dir_service = DirectoryService(nfs=nfs_attrs, enabled=False)
            else:
                changed = False
        if changed:
            res = blade.patch_directory_services(
                names=[ds_name], directory_service=dir_service
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="Delete Directory Service {0} failed. Error: {1}".format(
                        ds_name, get_error_message(res)
                    )
                )
    module.exit_json(changed=changed)


def update_ds(module, blade):
    """Update Directory Service"""
    mod_ds = False
    changed = False
    password_required = False
    attr = {}
    ds_name = module.params["dstype"]
    if module.params["nfs_server"] and module.params["dstype"] == "nfs":
        if module.params["nfs_server"] != "_array_server":
            ds_name = module.params["nfs_server"] + "_nfs"
    res = blade.get_directory_services(names=[ds_name])
    if res.status_code != 200:
        module.fail_json(
            msg="Fetch {0} Directory Service failed. Error: {1}".format(
                module.params["dstype"], get_error_message(res)
            )
        )
    ds_now = list(res.items)[0]
    # `enable` has no default, so a value is only applied when it was given.
    if (
        module.params["enable"] is not None
        and module.params["enable"] != ds_now.enabled
    ):
        attr["enabled"] = module.params["enable"]
        mod_ds = True
    if module.params["dstype"] == "nfs" and module.params["nis_servers"]:
        current_nis_servers = getattr(ds_now.nfs, "nis_servers", None) or []
        current_nis_domains = getattr(ds_now.nfs, "nis_domains", None) or []
        if sorted(module.params["nis_servers"]) != sorted(
            current_nis_servers
        ) or module.params["nis_domain"] != "".join(map(str, current_nis_domains)):
            attr["nfs"] = {
                "nis_domains": [module.params["nis_domain"]],
                "nis_servers": module.params["nis_servers"][0:30],
            }
            mod_ds = True
    elif module.params["dstype"] == "smb":
        if module.params["join_ou"] != ds_now.smb.join_ou:
            attr["smb"] = {"join_ou": module.params["join_ou"]}
            mod_ds = True
    else:
        if module.params["uri"]:
            if sorted(module.params["uri"][0:30]) != sorted(ds_now.uris or []):
                attr["uris"] = module.params["uri"][0:30]
                mod_ds = True
                password_required = True
        if module.params["base_dn"]:
            if module.params["base_dn"] != ds_now.base_dn:
                attr["base_dn"] = module.params["base_dn"]
                mod_ds = True
        if module.params["bind_user"]:
            if module.params["bind_user"] != ds_now.bind_user:
                password_required = True
                attr["bind_user"] = module.params["bind_user"]
                mod_ds = True
            elif (
                module.params["force_bind_password"] and module.params["bind_password"]
            ):
                # A password that was not supplied cannot be forced, so
                # "present with the current values" is a no-op without one.
                password_required = True
                mod_ds = True
        if password_required:
            if module.params["bind_password"]:
                attr["bind_password"] = module.params["bind_password"]
                mod_ds = True
            else:
                module.fail_json(msg="'bind_password' must be provided for this task")
    if mod_ds:
        changed = True
        if not module.check_mode:
            n_attr = DirectoryService(**attr)
            res = blade.patch_directory_services(
                names=[ds_name], directory_service=n_attr
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to change directory service {0}. Error: {1}".format(
                        ds_name, get_error_message(res)
                    )
                )
    module.exit_json(changed=changed)


def create_ds(module, blade):
    """Create Directory Service"""
    changed = True
    ds_name = module.params["dstype"]
    # A new service is created disabled unless `enable` says otherwise.
    enabled = bool(module.params["enable"])
    if module.params["dstype"] == "nfs":
        if not module.params["uri"] and not module.params["nis_domain"]:
            module.fail_json(
                msg="Incorrect parameters provided for dstype {0}".format(
                    module.params["dstype"]
                )
            )
    elif not module.params["uri"]:
        module.fail_json(
            msg="Incorrect parameters provided for dstype {0}".format(
                module.params["dstype"]
            )
        )
    if not module.check_mode:
        dir_service = DirectoryService()  # Initialize for pylint
        if module.params["dstype"] == "management":
            dir_service = DirectoryService(
                uris=module.params["uri"][0:30],
                base_dn=module.params["base_dn"],
                bind_user=module.params["bind_user"],
                bind_password=module.params["bind_password"],
                enabled=enabled,
            )
        elif module.params["dstype"] == "smb":
            smb_attrs = {"join_ou": module.params["join_ou"]}
            dir_service = DirectoryService(
                uris=module.params["uri"][0:30],
                base_dn=module.params["base_dn"],
                bind_user=module.params["bind_user"],
                bind_password=module.params["bind_password"],
                smb=smb_attrs,
                enabled=enabled,
            )
        elif module.params["dstype"] == "nfs":
            if module.params["nis_domain"]:
                nfs_attrs = {
                    "nis_domains": [module.params["nis_domain"]],
                    "nis_servers": module.params["nis_servers"][0:30],
                }
                dir_service = DirectoryService(nfs=nfs_attrs, enabled=enabled)
            else:
                dir_service = DirectoryService(
                    uris=module.params["uri"][0:30],
                    base_dn=module.params["base_dn"],
                    bind_user=module.params["bind_user"],
                    bind_password=module.params["bind_password"],
                    enabled=enabled,
                )
        if module.params["nfs_server"] and module.params["dstype"] == "nfs":
            if module.params["nfs_server"] != "_array_server":
                ds_name = module.params["nfs_server"] + "_nfs"
        res = blade.patch_directory_services(
            names=[ds_name], directory_service=dir_service
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Create Directory Service {0} failed. Error: {1}".format(
                    ds_name, get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def test_ds(module, blade):
    """Test directory services configuration"""
    test_server = module.params["dstype"]
    if module.params["dstype"] == "nfs" and module.params["nfs_server"]:
        if module.params["nfs_server"] != "_array_server":
            test_server = module.params["nfs_server"] + "_nfs"
    test_response = []
    response = list(blade.get_directory_services_test(names=[test_server]).items)
    for component in response:
        if component.enabled:
            enabled = "true"
        else:
            enabled = "false"
        if component.success:
            success = "true"
        else:
            success = "false"
        test_response.append(
            {
                "component_address": component.component_address,
                "component_name": component.component_name,
                "description": component.description,
                "destination": component.destination,
                "enabled": enabled,
                "result_details": getattr(component, "result_details", ""),
                "success": success,
                "test_type": component.test_type,
                "resource_name": component.resource.name,
            }
        )
    module.exit_json(changed=False, test_response=test_response)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            uri=dict(type="list", elements="str"),
            dstype=dict(
                required=True, type="str", choices=["management", "nfs", "smb"]
            ),
            state=dict(
                type="str", default="present", choices=["absent", "present", "test"]
            ),
            enable=dict(type="bool"),
            bind_password=dict(type="str", no_log=True),
            force_bind_password=dict(type="bool", default=True, no_log=False),
            bind_user=dict(type="str"),
            base_dn=dict(type="str"),
            join_ou=dict(type="str"),
            nis_domain=dict(type="str"),
            nis_servers=dict(type="list", elements="str"),
            nfs_server=dict(type="str"),
        )
    )

    # bind_password is deliberately not tied to uri: an existing service can
    # be declared with its current values without re-sending the secret.
    required_together = [
        ["uri", "bind_user", "base_dn"],
        ["nis_servers", "nis_domain"],
    ]
    mutually_exclusive = [["uri", "nis_domain"]]

    module = AnsibleModule(
        argument_spec,
        required_together=required_together,
        mutually_exclusive=mutually_exclusive,
        supports_check_mode=True,
    )
    if not HAS_PYPURECLIENT:
        module.fail_json(msg="py-pure-client sdk is required for this module")

    state = module.params["state"]
    blade = get_system(module)
    api_version = get_rest_api_version(blade)
    if (
        LooseVersion(NO_SMB_VERSION) <= LooseVersion(api_version)
        and module.params["dstype"] == "smb"
    ):
        module.warn("Directory Service for SMB no longer supported by FlashBlade")
        module.exit_json(changed=False)
    ds_name = module.params["dstype"]
    if module.params["nfs_server"] and module.params["dstype"] == "nfs":
        if module.params["nfs_server"] != "_array_server":
            ds_name = module.params["nfs_server"] + "_nfs"
    res = blade.get_directory_services(names=[ds_name])
    ds_configured = False
    if res.status_code == 200:
        dirserv = list(res.items)[0]
        # The built-in service objects always exist, so every state=present
        # request is a comparison against the current values in update_ds;
        # an LDAP-based nfs service must not be mistaken for an empty one
        # just because it has no NIS servers.
        ds_configured = True
    else:
        module.exit_json(msg="Directory Service {0} does not exist".format(ds_name))
    ldap_uri = False
    set_ldap = False
    for uri in dirserv.uris or []:
        if "ldap" in uri.lower():
            ldap_uri = True
    if module.params["uri"]:
        for uri in module.params["uri"]:
            if "ldap" in uri.lower():
                set_ldap = True
    if not module.params["uri"] and ldap_uri or module.params["uri"] and set_ldap:
        if module.params["nis_servers"] or module.params["nis_domain"]:
            module.fail_json(
                msg="NIS configuration not supported in an LDAP environment"
            )
    if state == "absent":
        delete_ds(module, blade)
    elif ds_configured and state == "present":
        update_ds(module, blade)
    elif not ds_configured and state == "present":
        create_ds(module, blade)
    elif state == "test":
        test_ds(module, blade)
    else:
        module.exit_json(changed=False)


if __name__ == "__main__":
    main()
