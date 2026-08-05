#!/usr/bin/python
# coding: utf-8 -*-

# Copyright: (c) 2024, John Westcott <john-westcott-iv>
# Copyright: (c) 2020,Geoffrey Bachelot <bachelotg@gmail.com>
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

from __future__ import absolute_import, division, print_function

__metaclass__ = type


DOCUMENTATION = '''
---
module: application
author: "John Westcott IV (@john-westcott-iv)"
short_description: create, update, or destroy Automation Platform gateway applications
description:
    - Create, update, or destroy Automation Platform gateway applications.
options:
    name:
      description:
        - Name of the application.
      required: True
      type: str
    new_name:
      description:
        - Setting this option will change the existing name (looked up via the name field.
      type: str
    description:
      description:
        - Description of the application.
      type: str
    algorithm:
      description:
        - "The OIDC token signing algorithm for this application"
      choices: ["", "RS256", "HS256"]
      type: str
      required: False
    authorization_grant_type:
      description:
        - The grant type the user must use for acquire tokens for this application.
      choices: ["password", "authorization-code"]
      type: str
      required: False
    client_type:
      description:
        - Set to public or confidential depending on how secure the client device is.
      choices: ["public", "confidential"]
      type: str
      required: False
    organization:
      description: The name or ID referencing the Organization
      type: str
      required: True
    new_organization:
      type: str
      description: Setting this option will change the existing organization (looked up via the organization field)
    post_logout_redirect_uris:
      description:
        - Allowed Post Logout URIs list, space separated
      type: list
      elements: str
    redirect_uris:
      description:
        - Allowed urls list, space separated. Required when authorization-grant-type=authorization-code
      type: list
      elements: str
    app_url:
      description:
        - The URL of the application.
      type: str
      required: False
    state:
      description:
        - Desired state of the resource.
      default: "present"
      choices: ["present", "absent", "exists", "enforced"]
      type: str
    skip_authorization:
      description:
        - Set True to skip authorization step for completely trusted applications.
      type: bool
    user:
      description:
        - "The name or ID of the user who owns this application"
      type: str
      required: False

extends_documentation_fragment: ansible.platform.auth
'''


EXAMPLES = '''
- name: Add Foo application
  ansible.platform.application:
    name: "Foo"
    description: "Foo bar application"
    organization: "test"
    state: present
    authorization_grant_type: password
    client_type: public
    app_url: http://example.com

- name: Add Foo application
  ansible.platform.application:
    name: "Foo"
    description: "Foo bar application"
    organization: "test"
    state: present
    authorization_grant_type: authorization-code
    client_type: confidential
    redirect_uris:
      - http://example.com/api/gateway/v1/
    app_url: http://example.com
...
'''

from ..module_utils.aap_application import AAPApplication
from ..module_utils.aap_module import AAPModule


def main():
    # Any additional arguments that are not fields of the item can be added here
    argument_spec = dict(
        name=dict(required=True),
        new_name=dict(),
        organization=dict(required=True),
        new_organization=dict(type="str"),
        description=dict(),
        authorization_grant_type=dict(choices=["password", "authorization-code"]),
        client_type=dict(choices=['public', 'confidential']),
        redirect_uris=dict(type="list", elements='str'),
        skip_authorization=dict(type='bool'),
        algorithm=dict(choices=["", "RS256", "HS256"]),
        post_logout_redirect_uris=dict(type="list", elements="str"),
        app_url=dict(type="str"),
        user=dict(type="str"),
        state=dict(choices=["present", "absent", "exists", "enforced"], default="present"),
    )

    # Create a module for ourselves
    module = AAPModule(argument_spec=argument_spec)
    AAPApplication(module).manage(json_output_fields=['client_id', 'client_secret'])


if __name__ == '__main__':
    main()
