#!/usr/bin/env python

import importlib
import os
import re
import sys

import requests
import urllib3
import yaml

urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)

# Analysis variables
# -----------------------------------------------------------------------------------------------------------

# Read-only endpoints are dynamically created by an options page with no POST section.
# Normally a read-only endpoint should not have a module (i.e. /api/v2/me) but sometimes we reuse a name
# For example, we have a role module but /api/v2/roles is a read only endpoint.
# This list indicates which read-only endpoints have associated modules with them.
read_only_endpoints_with_modules = ['settings', 'authenticator_user']

# If a module should not be created for an endpoint and the endpoint is not read-only add it here
# THINK HARD ABOUT DOING THIS
no_module_for_endpoint = []

# Some modules work on the related fields of an endpoint. These modules will not have an auto-associated endpoint
no_endpoint_for_module = ['token']

# Add modules with endpoints that are not at /api/v2
extra_endpoints = {}

# Global module parameters we can ignore
ignore_module_parameters = ['state', 'new_name', 'new_organization', 'new_authenticator', 'update_secrets', 'copy_from']
ignore_api_parameters = {
    'team': ['users', 'admins'],  # TODO: remove when removed from API
    'organization': ['users', 'admins'],  # TODO: remove when removed from API
}

# Some modules take additional parameters that do not appear in the API
# Add the module name as the key with the value being the list of params to ignore
no_api_parameter_ok = {
    # Existing_token and id are for working with an existing tokens
    'token': ['existing_token', 'existing_token_id', 'organization'],
}

# When this tool was created we were not feature complete. Adding something in here indicates a module
# that needs to be developed. If the module is found on the file system it will auto-detect that the
# work is being done and will bypass this check. At some point this module should be removed from this list.
# https://issues.redhat.com/browse/AAP-23122 for DAB RBAC endpoints
# https://issues.redhat.com/browse/AAP-24613 for service_key
needs_development = ['role_definition', 'role_team_assignment']  # i.e. 'team', 'organization'
needs_param_development = {}
# -----------------------------------------------------------------------------------------------------------

base_dir = os.path.abspath(os.path.join(os.path.dirname(__file__), os.pardir))
sys.path.append(base_dir)
return_value = 0
read_only_endpoint = []

# Error output
failed_modules = []


def test_meta_runtime():
    meta_filename = 'meta/runtime.yml'

    print("\n=======================\nmeta/runtime.yml check:\n-----------------------")

    with open('{0}/{1}'.format(base_dir, meta_filename), 'r') as f:
        meta_data_string = f.read()

    meta_data = yaml.load(meta_data_string, Loader=yaml.Loader)

    action_groups = meta_data.get('action_groups', {}).get('gateway', [])
    needs_to_be_removed = list(set(action_groups) - set(needs_grouping))
    needs_to_be_added = list(set(needs_grouping) - set(action_groups))

    needs_to_be_removed.sort()
    needs_to_be_added.sort()

    group = 'action-groups.gateway'
    if needs_to_be_removed:
        print(
            cause_error(
                "Meta/runtime.yml check",
                "The following items should be removed from the {0} {1}:\n    {2}".format(meta_filename, group, '\n    '.join(needs_to_be_removed)),
            )
        )

    if needs_to_be_added:
        print(
            cause_error(
                "Meta/runtime.yml check",
                "The following items should be added to the {0} {1}:\n    {2}".format(meta_filename, group, '\n    '.join(needs_to_be_added)),
            )
        )

    if not needs_to_be_added or needs_to_be_removed:
        print("OK\n")


def cause_error(module_name, msg):
    global return_value
    return_value = 255
    try:
        failed_modules.index(module_name)
    except ValueError:
        failed_modules.append(module_name)
    return msg


def determine_state(module_id, endpoint, module, parameter, api_option, module_option):
    # This is a hierarchical list of things that are ok/failures based on conditions
    # If we know this module needs development this is a non-blocking failure
    if module_id in needs_development and module == 'N/A':
        return "Warning, module needs development"

    # If the module is a read only endpoint:
    #    If it has no module on disk that is ok.
    #    If it has a module on disk, but it's listed in read_only_endpoints_with_modules that is ok
    #    Else we have a module for a read only endpoint that should not exit
    if module_id in read_only_endpoint:
        if module == 'N/A':
            # There may be some cases where a read only endpoint has a module
            return "OK, this endpoint is read-only and should not have a module"
        elif module_id in read_only_endpoints_with_modules:
            return "OK, module params can not be checked to read-only"
        else:
            return cause_error(module_id, "Failed, read-only endpoint should not have an associated module")

    # If the endpoint is listed as not needing a module and we don't have one we are ok
    if module_id in no_module_for_endpoint and module == 'N/A':
        return "OK, this endpoint should not have a module"

    # If module is listed as not needing an endpoint and we don't have one we are ok
    if module_id in no_endpoint_for_module and endpoint == 'N/A':
        return "OK, this module does not require an endpoint"

    # All the end/point module conditionals are done so if we don't have a module or endpoint we have a problem
    if module == 'N/A':
        return cause_error(module_id, 'Failed, missing module')
    if endpoint == 'N/A':
        return cause_error(module_id, 'Failed, why does this module have no endpoint')

    # Now perform parameter checks

    # First, if the parameter is in the ignore_module_parameters list we are ok
    if parameter in ignore_module_parameters:
        return "OK, globally ignored module parameter"

    # Second, if the parameter is in the ignore_api_parameters list are ok to ignore
    if parameter in ignore_api_parameters.get(module, []):
        return "OK, ignored api parameter"

    # Third, if this is a read only parameter we are ok to ignore
    if api_option and api_option['read_only']:
        return "OK, read only api parameters"

    # If both the api option and the module option are both either objects or none
    if (api_option is None) ^ (module_option is None):
        # If the API option is node and the parameter is in the no_api_parameter list we are ok
        if api_option is None and parameter in no_api_parameter_ok.get(module, {}):
            return 'OK, no api parameter is ok'
        # If we know this parameter needs development and we don't have a module option we are non-blocking
        if module_option is None and parameter in needs_param_development.get(module_id, {}):
            return "Failed (non-blocking), parameter needs development"
        # Check for deprecated in the node, if its deprecated and has no api option we are ok, otherwise we have a problem
        if module_option and module_option.get('description'):
            description = ''
            if isinstance(module_option.get('description'), str):
                description = module_option.get('description')
            else:
                description = " ".join(module_option.get('description'))

            if 'deprecated' in description.lower():
                if api_option is None:
                    return 'OK, deprecated module option'
                else:
                    return cause_error(module_id, 'Failed, module marks option as deprecated but option still exists in API')
        # If we don't have a corresponding API option but we are a list then we are likely a relation
        if not api_option and module_option and module_option.get('type', 'str') == 'list':
            return "OK, Field appears to be relation"
            # TODO, at some point try and check the object model to confirm its actually a relation

        return cause_error(module_id, 'Failed, option mismatch')

    # We made it through all the checks, so we are ok
    return 'OK'


# Load the container-startup.yml file
with open(os.path.join(base_dir, os.pardir, 'container-startup.yml'), 'r') as f:
    container_startup_info = yaml.safe_load(f)

option_comparison = {}
# Load a list of existing module files from disk
module_directory = os.path.join(base_dir, 'plugins', 'modules')
sys.path.append(module_directory)
needs_grouping = []

for root, dirs, files in os.walk(module_directory):
    if root == module_directory:
        for filename in files:
            file = os.path.join(root, filename)
            if os.path.islink(file):
                continue
            # must begin with a letter a-z, and end in .py
            if re.match(r'^[a-z].*.py$', filename):
                module_name = filename[:-3]
                resource_module = importlib.import_module(f'plugins.modules.{module_name}')

                option_comparison[module_name] = {
                    'endpoint': 'N/A',
                    'api_options': {},
                    'module_options': {},
                    'module_name': module_name,
                }
                try:
                    documentation = yaml.load(resource_module.DOCUMENTATION, Loader=yaml.SafeLoader)
                    option_comparison[module_name]['module_options'] = documentation.get('options', {})
                    if 'ansible.platform.auth' in documentation.get('extends_documentation_fragment', []):
                        needs_grouping.append(module_name)

                except yaml.parser.ParserError as e:
                    print(f"Failed to load documentation for {module_name}: {e}")

request_session = requests.Session()
request_session.auth = (container_startup_info['gateway_admin_username'], container_startup_info['gateway_admin_password'])

endpoint_response = request_session.get(f"{container_startup_info['gateway_host']}/api/gateway/v1/", verify=False)

json_response = endpoint_response.json()
# Append any extra_endpoints to the list of endpoints to validate
for key, val in extra_endpoints.items():
    json_response[key] = val

for endpoint in json_response.keys():
    # Module names are singular and endpoints are plural, so we need to convert to singular
    singular_endpoint = '{0}'.format(endpoint)
    if singular_endpoint.endswith('ies'):
        singular_endpoint = singular_endpoint[:-3]
    if singular_endpoint != 'settings' and singular_endpoint.endswith('s'):
        singular_endpoint = singular_endpoint[:-1]
    module_name = '{0}'.format(singular_endpoint)

    endpoint_url = json_response.get(endpoint)

    # If we don't have a module for this endpoint then we can create an empty one
    if module_name not in option_comparison:
        option_comparison[module_name] = {}
        option_comparison[module_name]['module_name'] = 'N/A'
        option_comparison[module_name]['module_options'] = {}

    # Add in our endpoint and an empty api_options
    option_comparison[module_name]['endpoint'] = endpoint_url
    option_comparison[module_name]['api_options'] = {}

    # Get out the endpoint, load and parse its options page
    options_response = request_session.options(f"{container_startup_info['gateway_host']}{endpoint_url}", verify=False)
    if 'POST' in options_response.json().get('actions', {}):
        option_comparison[module_name]['api_options'] = options_response.json().get('actions').get('POST')
    else:
        read_only_endpoint.append(module_name)

# Parse through our data to get string lengths to make a pretty report
longest_module_name = 0
longest_option_name = 0
longest_endpoint = 0
for module, module_value in option_comparison.items():
    if len(module_value['module_name']) > longest_module_name:
        longest_module_name = len(module_value['module_name'])
    if len(module_value['endpoint']) > longest_endpoint:
        longest_endpoint = len(module_value['endpoint'])
    for option in module_value['api_options'], module_value['module_options']:
        if len(option) > longest_option_name:
            longest_option_name = len(option)

# Print out some headers
print(
    "".join(
        [
            "End Point",
            " " * (longest_endpoint - len("End Point")),
            " | Module Name",
            " " * (longest_module_name - len("Module Name")),
            " | Option",
            " " * (longest_option_name - len("Option")),
            " | API | Module | State",
        ]
    )
)


def table_separator_line(char='-'):
    print(
        f"{char}|{char}".join(
            [
                f"{char}" * longest_endpoint,
                f"{char}" * longest_module_name,
                f"{char}" * longest_option_name,
                f"{char}" * 3,
                f"{char}" * 6,
                f"{char}" * 45,
            ]
        )
    )


table_separator_line("=")


# Print out all of our data
for module in sorted(option_comparison):
    first_line = True

    module_data = option_comparison[module]
    all_param_names = list(set(module_data['api_options']) | set(module_data['module_options']))
    for parameter in sorted(all_param_names):
        if first_line:
            endpoint_name, endpoint_spaces_cnt = module_data['endpoint'], longest_endpoint - len(module_data['endpoint'])
            module_name, module_spaces_cnt = module_data['module_name'], longest_module_name - len(module_data['module_name'])
            first_line = False
        else:
            endpoint_name, endpoint_spaces_cnt = '', longest_endpoint
            module_name, module_spaces_cnt = '', longest_module_name

        print(
            "".join(
                [
                    endpoint_name,
                    " " * endpoint_spaces_cnt,
                    " | ",
                    module_name,
                    " " * module_spaces_cnt,
                    " | ",
                    parameter,
                    " " * (longest_option_name - len(parameter)),
                    " | ",
                    " X " if (parameter in module_data['api_options']) else '   ',
                    " | ",
                    '  X   ' if (parameter in module_data['module_options']) else '      ',
                    " | ",
                    determine_state(
                        module,
                        module_data['endpoint'],
                        module_data['module_name'],
                        parameter,
                        module_data['api_options'][parameter] if (parameter in module_data['api_options']) else None,
                        module_data['module_options'][parameter] if (parameter in module_data['module_options']) else None,
                    ),
                ]
            )
        )
    # This handles cases were we got no params from the options page nor from the modules
    if len(all_param_names) == 0:
        print(
            "".join(
                [
                    module_data['endpoint'],
                    " " * (longest_endpoint - len(module_data['endpoint'])),
                    " | ",
                    module_data['module_name'],
                    " " * (longest_module_name - len(module_data['module_name'])),
                    " | ",
                    "N/A",
                    " " * (longest_option_name - len("N/A")),
                    " | ",
                    '   ',
                    " | ",
                    '      ',
                    " | ",
                    determine_state(module, module_data['endpoint'], module_data['module_name'], 'N/A', None, None),
                ]
            )
        )

    table_separator_line("-")

test_meta_runtime()

if return_value == 255:
    print("=======================\nSummary: Failed modules\n-----------------------")
    for module_name in failed_modules:
        print(f"- {module_name}")


sys.exit(return_value)
