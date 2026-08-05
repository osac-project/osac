#!/usr/bin/python
# coding: utf-8 -*-

# (c) 2023, Sean Sullivan <@sean-m-sullivan>
# (c) 2018, Nikhil Jain <nikjain@redhat.com>
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

from __future__ import absolute_import, division, print_function

__metaclass__ = type


DOCUMENTATION = """
---
module: settings
author: Sean Sullivan (@sean-m-sullivan)
short_description: Modify automation platform gateway settings.
description:
    - Modify automation platform gateway settings.
options:
    settings:
      description:
        - A data structure to be sent into the settings endpoint
      type: dict
      required: True
extends_documentation_fragment: ansible.platform.auth
"""

EXAMPLES = """
- name: Make changes to the settings in automation platform gateway
  ansible.platform.settings:
    settings:
      gateway_token_name: X-DAB-JW-TOKEN
      gateway_access_token_expiration: 600
      gateway_basic_auth_enabled: true
      gateway_proxy_url: https://localhost:9080
      gateway_proxy_url_ignore_cert: false
      password_min_length: 0
      password_min_digits: 0
      password_min_upper: 0
      password_min_special: 0
      allow_admins_to_set_insecure: false
...
"""

from ..module_utils.aap_module import AAPModule


def main():
    # Any additional arguments that are not fields of the item can be added here
    argument_spec = dict(
        settings=dict(required=True, type="dict"),
    )

    # Create a module for ourselves
    module = AAPModule(argument_spec=argument_spec, supports_check_mode=True)

    new_settings = module.params.get("settings")

    # Load the existing settings
    existing_settings = module.get_endpoint("settings/all")["json"]

    # Begin a json response
    json_output = {"changed": False, "old_values": {}, "new_values": {}}

    # Check any of the settings to see if anything needs to be updated
    needs_update = False
    for a_setting in new_settings:
        if a_setting not in existing_settings or existing_settings[a_setting] != new_settings[a_setting]:
            # At least one thing is different so we need to patch
            needs_update = True
            json_output["old_values"][a_setting] = existing_settings[a_setting]
            json_output["new_values"][a_setting] = new_settings[a_setting]

    if module._diff:
        json_output["diff"] = {"before": json_output["old_values"], "after": json_output["new_values"]}

    # If nothing needs an update we can simply exit with the response (as not changed)
    if not needs_update:
        module.exit_json(**json_output)

    if module.check_mode and module._diff:
        json_output["changed"] = True
        module.exit_json(**json_output)

    # Make the call to update the settings
    url = module.build_url("settings/all")
    response = module.make_request("PUT", url, **{"data": new_settings})

    if response["status_code"] == 200:
        # Set the changed response to True
        json_output["changed"] = True

        # To deal with the old style values we need to return 'value' in the response
        new_values = {}
        for a_setting in new_settings:
            new_values[a_setting] = response["json"][a_setting]

        module.exit_json(**json_output)
    elif "json" in response and "__all__" in response["json"]:
        module.fail_json(msg=response["json"]["__all__"])
    else:
        module.fail_json(**{"msg": "Unable to update settings, see response", "response": response})


if __name__ == "__main__":
    main()
