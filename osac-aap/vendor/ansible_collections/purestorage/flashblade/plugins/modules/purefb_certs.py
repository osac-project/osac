#!/usr/bin/python
# -*- coding: utf-8 -*-

# (c) 2024, Simon Dodsley (simon@purestorage.com)
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
module: purefb_certs
version_added: '1.4.0'
short_description: Manage FlashBlade SSL Certificates
description:
- Create, delete, import and export FlashBlade SSL Certificates
author:
- Everpure Ansible Team (@sdodsley) <pure-ansible-team@purestorage.com>
options:
  name:
    description:
    - Name of the SSL Certificate
    type: str
    required: true
  state:
    description:
    - Action for the module to perform
    - I(present) will create or import an SSL certificate including self signed certificates
    - I(absent) will delete an existing SSL certificate
    - I(sign) will construct a Certificate Signing request (CSR) from an existing (self signed) certificate
    - I(export) will export the existing SSL certificate
    - I(import) will import or create a provided certificate.
    default: present
    choices: [ absent, present, import, export, sign ]
    type: str
  certificate_type:
    description:
    - Type can be I(array) for FlashBlade as server or I(external) for FlashBlade as client (eg access to AD).
    type: str
    choices: [ external, array ]
  certificate:
    aliases: [ contents ]
    type: str
    description:
    - Required for I(import)
    - A valid signed certicate in PEM format (Base64 encoded)
    - Includes the "-----BEGIN CERTIFICATE-----" and "-----END CERTIFICATE-----" lines
    - Does not exceed 3000 characters in length
  intermediate_cert:
    aliases: [ intermeadiate_cert ]
    type: str
    description:
    - Intermeadiate certificate provided by the CA
  key:
    aliases: [ private_key ]
    type: str
    description:
    - Certificates of type I(array) must have a private key
    - If the Certificate Signed Request (CSR) was not constructed on the system
      or the private key has changed since construction of the CSR, provide
      a new private key here
  passphrase:
    type: str
    description:
    - Passphrase if the private key is encrypted
  export_file:
    type: str
    description:
    - Name of file to contain Certificate Signing Request when `status sign`
    - Name of file to export the current SSL Certificate when `status export`
    - File will be overwritten if it already exists
  country:
    type: str
    description:
    - The two-letter ISO code for the country where your organization is located
  province:
    type: str
    description:
    - The full name of the state or province where your organization is located
  locality:
    type: str
    description:
    - The full name of the city where your organization is located
  organization:
    type: str
    description:
    - The full and exact legal name of your organization.
    - The organization name should not be abbreviated and should
      include suffixes such as Inc, Corp, or LLC.
  org_unit:
    type: str
    description:
    - The department within your organization that is managing the certificate
  common_name:
    type: str
    description:
    - The fully qualified domain name (FQDN) of the current system
    - For example, the common name for https://pureblade.example.com is
      pureblade.example.com, or *.example.com for a wildcard certificate
    - This can also be the management IP address of the system or the
      shortname of the current system.
    - Maximum of 64 characters
    - If not provided this will default to the shortname of the system
  email:
    type: str
    description:
    - The email address used to contact your organization
  key_size:
    type: int
    description:
    - The key size in bits if you generate a new private key
    choices: [ 1024, 2048, 4096 ]
  key_algorithm:
    type: str
    description:
    - The key algorithm used to generate the certificate.
    choices: [ rsa, ec, ed448, ed25519 ]
  days:
    type: int
    description:
    - The number of valid days for the self-signed certificate being generated
    - If not specified, the self-signed certificate expires after 3650 days.
  generate:
    default: false
    type: bool
    description:
    - Generate a new private key.
    - If not selected, the certificate will use the existing key
    - Required when changing key-size or key-algorithm
  subject_alternative_names:
    type: list
    elements: str
    description:
    - The alternative names that are secured by this certificate.
    - Alternative names may be IP addresses, DNS names, or URIs.
    version_added: "1.22.0"
extends_documentation_fragment:
- purestorage.flashblade.purestorage.fb
"""

EXAMPLES = r"""
- name: Create self-signed SSL certifcate foo
  purestorage.flashblade.purefb_certs:
    name: foo
    key_size: 4096
    country: US
    province: FL
    locality: Miami
    organization: "Acme Inc"
    org_unit: "DevOps"
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Delete SSL certificate foo
  purestorage.flashblade.purefb_certs:
    name: foo
    state: absent
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Request CSR
  purestorage.flashblade.purefb_certs:
    name: foo
    state: sign
    export_file: <filepath>
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Request CSR with updated fields
  purestorage.flashblade.purefb_certs:
    name: foo
    state: sign
    export_file: <filepath>
    org_unit: Development
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Regenerate key for self-signed SSL foo
  purestorage.flashblade.purefb_certs:
    generate: true
    name: foo
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Import SSL Cert foo and Private Key
  purestorage.flashblade.purefb_certs:
    state: import
    name: foo
    certificate: "{{lookup('file', 'example.crt') }}"
    key: "{{lookup('file', 'example.key') }}"
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641
"""

RETURN = r"""
"""

HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import (
        CertificatePost,
        CertificateSigningRequestPost,
        Reference,
        CertificatePatch,
    )
except ImportError:
    HAS_PYPURECLIENT = False

HAS_PYCOUNTRY = True
try:
    import pycountry
except ImportError:
    HAS_PYCOUNTRY = False

import re
from ansible.module_utils.basic import AnsibleModule
from ansible_collections.purestorage.flashblade.plugins.module_utils.purefb import (
    get_system,
    purefb_argument_spec,
)
from ansible_collections.purestorage.flashblade.plugins.module_utils.common import (
    get_error_message,
)

CERT_TYPE_VERSION = "2.15"
CSR_API_VERSION = "2.20"


def update_cert(module, blade):
    """Update existing SSL Certificate"""
    api_versions = list(blade.get_versions().items)

    if CSR_API_VERSION in api_versions:
        changed = True
        certificate = CertificatePatch(
            certificate=module.params["certificate"],
            intermediate_certificate=module.params["intermediate_cert"],
            private_key=module.params["key"],
            passphrase=module.params["passphrase"],
            common_name=module.params["common_name"],
            country=module.params["country"],
            email=module.params["email"],
            key_size=module.params["key_size"],
            locality=module.params["locality"],
            organization=module.params["organization"],
            organizational_unit=module.params["org_unit"],
            state=module.params["province"],
            days=module.params["days"],
            subject_alternative_names=module.params["subject_alternative_names"],
        )
        if not module.check_mode:
            generate = None
            if module.params["generate"]:
                generate = "True"
            res = blade.patch_certificates(
                names=[module.params["name"]],
                generate_new_key=generate,
                certificate=certificate,
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="Updating existing SSL certificate {0} failed. Error: {1}".format(
                        module.params["name"],
                        get_error_message(res),
                    )
                )
    else:
        changed = True
        certificate = CertificatePatch(
            certificate=module.params["certificate"],
            intermediate_certificate=module.params["intermediate_cert"],
            private_key=module.params["key"],
            passphrase=module.params["passphrase"],
        )
        if not module.check_mode:
            res = blade.patch_certificates(
                names=[module.params["name"]], certificate=certificate
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="Updating existing SSL certificate {0} failed. Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )
    module.exit_json(changed=changed)


def create_cert(module, blade):
    # let the rest-api itself deal with errors
    changed = True
    api_versions = list(blade.get_versions().items)
    if CSR_API_VERSION in api_versions:
        certificate = CertificatePost(
            certificate_type=module.params["certificate_type"],
            certificate=module.params["certificate"],
            intermediate_certificate=module.params["intermediate_cert"],
            private_key=module.params["key"],
            passphrase=module.params["passphrase"],
            common_name=module.params["common_name"],
            country=module.params["country"],
            email=module.params["email"],
            key_size=module.params["key_size"],
            locality=module.params["locality"],
            organization=module.params["organization"],
            organizational_unit=module.params["org_unit"],
            state=module.params["province"],
            days=module.params["days"],
            subject_alternative_names=module.params["subject_alternative_names"],
        )
    else:
        if CERT_TYPE_VERSION in api_versions:
            certificate = CertificatePost(
                certificate_type=module.params["certificate_type"],
                certificate=module.params["certificate"],
                intermediate_certificate=module.params["intermediate_cert"],
                private_key=module.params["key"],
                passphrase=module.params["passphrase"],
            )
        else:
            certificate = CertificatePost(
                certificate=module.params["certificate"],
                intermediate_certificate=module.params["intermediate_cert"],
                private_key=module.params["key"],
                passphrase=module.params["passphrase"],
            )

    if not module.check_mode:
        res = blade.post_certificates(
            names=[module.params["name"]], certificate=certificate
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Creating SSL certificate {0} failed. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )

    module.exit_json(changed=changed)


def delete_cert(module, blade):
    changed = True
    if module.params["name"] == "global":
        module.fail_json(msg="Global certificate cannot be deleted")
    if not module.check_mode:
        res = blade.delete_certificates(names=[module.params["name"]])
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to delete {0} SSL certificate. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def export_cert(module, blade):
    """Export current SSL certificate"""
    changed = True
    if not module.check_mode:
        ssl = blade.get_certificates(names=[module.params["name"]])
        if ssl.status_code != 200:
            module.fail_json(
                msg="Exporting Certificate failed. Error: {0}".format(
                    ssl.errors[0].message
                )
            )
        with open(module.params["export_file"], "w", encoding="utf-8") as ssl_file:
            ssl_file.write(list(ssl.items)[0].certificate)
    module.exit_json(changed=changed)


def create_csr(module, blade):
    """Construct a Certificate Signing Request

    Output the result to a specified file
    """
    changed = True
    if not module.check_mode:
        certificate = CertificateSigningRequestPost(
            certificate=Reference(name=module.params["name"]),
            common_name=module.params["common_name"],
            country=module.params["country"],
            email=module.params["email"],
            locality=module.params["locality"],
            organization=module.params["organization"],
            organizational_unit=module.params["org_unit"],
            state=module.params["province"],
            subject_alternative_names=module.params["subject_alternative_names"],
        )
        csr = list(
            blade.post_certificates_certificate_signing_requests(
                certificate=certificate
            ).items
        )[0].certificate_signing_request
        with open(module.params["export_file"], "w", encoding="utf-8") as csr_file:
            csr_file.writelines(list(csr))
    module.exit_json(changed=changed)


def main():
    # Do not set defaults, if you do you will have to craft each call to make sure
    # you are not trying to write values that are read-only given the other parameters
    # Exceptions: state, you must want to do some action
    #
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            state=dict(
                type="str",
                default="present",
                choices=["absent", "present", "import", "export", "sign"],
            ),
            generate=dict(type="bool", default=False),
            name=dict(type="str", required=True),
            certificate_type=dict(type="str", choices=["external", "array"]),
            country=dict(type="str"),
            province=dict(type="str"),
            locality=dict(type="str"),
            organization=dict(type="str"),
            org_unit=dict(type="str"),
            common_name=dict(type="str"),
            email=dict(type="str"),
            key_size=dict(type="int", choices=[1024, 2048, 4096]),
            certificate=dict(type="str", no_log=True, aliases=["contents"]),
            intermediate_cert=dict(
                type="str", no_log=True, aliases=["intermeadiate_cert"]
            ),
            key=dict(type="str", no_log=True, aliases=["private_key"]),
            export_file=dict(type="str"),
            passphrase=dict(type="str", no_log=True),
            days=dict(type="int"),
            key_algorithm=dict(type="str", choices=["rsa", "ec", "ed448", "ed25519"]),
            subject_alternative_names=dict(type="list", elements="str"),
        )
    )

    mutually_exclusive = [["certificate", "key_size"]]
    required_if = [
        ["state", "import", ["certificate"]],
        ["state", "export", ["export_file"]],
        ["state", "sign", ["export_file"]],
    ]

    module = AnsibleModule(
        argument_spec,
        mutually_exclusive=mutually_exclusive,
        required_if=required_if,
        supports_check_mode=True,
    )

    if not HAS_PYPURECLIENT:
        module.fail_json(msg="py-pure-client sdk is required for this module")

    if not HAS_PYCOUNTRY:
        module.fail_json(msg="pycountry sdk is required for this module")

    email_pattern = r"^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$"
    blade = get_system(module)
    api_versions = list(blade.get_versions().items)

    if module.params["email"]:
        if not re.search(email_pattern, module.params["email"]):
            module.fail_json(
                msg="Email {0} is not valid".format(module.params["email"])
            )
    if module.params["country"]:
        if len(module.params["country"]) != 2:
            module.fail_json(msg="Country must be a two-letter country (ISO) code")
        if not pycountry.countries.get(alpha_2=module.params["country"].upper()):
            module.fail_json(
                msg="Country code {0} is not an assigned ISO 3166-1 code".format(
                    module.params["country"].upper()
                )
            )

    if not module.params["certificate_type"]:
        if module.params["key"] or not module.params["certificate"]:
            module.params["certificate_type"] = "array"
        else:
            module.params["certificate_type"] = "external"

    state = module.params["state"]
    certificate_type = module.params["certificate_type"]

    exists = bool(
        blade.get_certificates(names=[module.params["name"]]).status_code == 200
    )

    if not exists and state == "present":
        create_cert(module, blade)
    elif exists and state == "present":
        update_cert(module, blade)
    elif state == "sign":
        if CSR_API_VERSION not in api_versions:
            module.fail_json(msg="Purity//FB 4.6.3+ is required for CSRs")
        create_csr(module, blade)
    elif not exists and state == "import":
        create_cert(module, blade)
    elif exists and state == "import" and certificate_type == "external":
        module.fail_json(msg="External Certificates cannot be reimported")
    elif exists and state == "import":
        update_cert(module, blade)
    elif state == "export":
        export_cert(module, blade)
    elif exists and state == "absent":
        delete_cert(module, blade)

    module.exit_json(changed=False)


if __name__ == "__main__":
    main()
