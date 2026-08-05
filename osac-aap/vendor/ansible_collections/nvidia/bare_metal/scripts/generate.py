#!/usr/bin/env python
# SPDX-FileCopyrightText: Copyright (c) 2026 Fabien Dupont
# SPDX-License-Identifier: Apache-2.0

"""Generate Ansible modules from the NVIDIA Bare Metal Manager OpenAPI spec.

Usage:
    python scripts/generate.py --spec ../bare-metal-manager-rest/openapi/spec.yaml --output plugins/modules
"""

from __future__ import absolute_import, division, print_function

import argparse
import os
import re
import sys
import textwrap

import yaml

# Allow importing from scripts/ directory
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from resource_config import (
    RESOURCE_OVERRIDES,
    READ_ONLY_TAGS,
    SKIP_TAGS,
    TAG_TO_MODULE,
    SKIP_PATHS,
    BATCH_MODULES,
    INFO_ONLY_MODULES,
    ACTION_MODULES,
)


# ---------------------------------------------------------------------------
# Utility helpers
# ---------------------------------------------------------------------------

# Matches {someParam} placeholders in URL paths.
_PATH_PARAM_RE = re.compile(r'\{(\w+)\}')


def camel_to_snake(name):
    """Convert camelCase to snake_case."""
    s1 = re.sub(r'([A-Z]+)([A-Z][a-z])', r'\1_\2', name)
    s2 = re.sub(r'([a-z0-9])([A-Z])', r'\1_\2', s1)
    return s2.lower()


def tag_to_module_name(tag):
    """Convert an OpenAPI tag name to a snake_case module name."""
    if tag in TAG_TO_MODULE:
        return TAG_TO_MODULE[tag]
    return camel_to_snake(tag.replace(' ', ''))


def resolve_ref(spec, ref):
    """Resolve a $ref string to the referenced object in the spec."""
    if not ref.startswith('#/'):
        return {}
    parts = ref.lstrip('#/').split('/')
    obj = spec
    for part in parts:
        if isinstance(obj, dict):
            obj = obj.get(part, {})
        else:
            return {}
    return obj


def resolve_refs_recursive(spec, obj, depth=0):
    """Recursively resolve all $ref in an object."""
    if depth > 20:
        return obj
    if isinstance(obj, dict):
        if '$ref' in obj:
            referenced = resolve_ref(spec, obj['$ref'])
            # Merge any sibling keys (like description) with the resolved ref
            merged = dict(resolve_refs_recursive(spec, referenced, depth + 1))
            for k, v in obj.items():
                if k != '$ref':
                    merged[k] = v
            return merged
        return {k: resolve_refs_recursive(spec, v, depth + 1) for k, v in obj.items()}
    if isinstance(obj, list):
        return [resolve_refs_recursive(spec, item, depth + 1) for item in obj]
    return obj


# ---------------------------------------------------------------------------
# OpenAPI schema -> Ansible argument_spec conversion
# ---------------------------------------------------------------------------

def openapi_type_to_ansible(schema, spec):
    """Convert an OpenAPI property schema to an Ansible argument_spec entry."""
    if not schema:
        return {'type': 'str'}

    # Resolve any $ref
    if '$ref' in schema:
        schema = resolve_refs_recursive(spec, schema)

    # Handle OpenAPI 3.1 nullable: type is a list like ["string", "null"]
    schema_type = schema.get('type', 'string')
    if isinstance(schema_type, list):
        non_null = [t for t in schema_type if t != 'null']
        schema_type = non_null[0] if non_null else 'string'

    # String enum (status types, etc.)
    if schema_type == 'string' and 'enum' in schema:
        return {'type': 'str', 'choices': schema['enum']}

    type_map = {
        'string': 'str',
        'integer': 'int',
        'boolean': 'bool',
        'number': 'float',
    }

    if schema_type in type_map:
        return {'type': type_map[schema_type]}

    # Object with additionalProperties (e.g., Labels)
    if schema_type == 'object' and 'additionalProperties' in schema:
        return {'type': 'dict'}

    # Object with properties -> nested dict with suboptions
    if schema_type == 'object' and 'properties' in schema:
        suboptions = {}
        required_fields = schema.get('required', [])
        for prop_name, prop_schema in schema.get('properties', {}).items():
            if prop_schema.get('readOnly'):
                continue
            entry = openapi_type_to_ansible(prop_schema, spec)
            entry_snake = camel_to_snake(prop_name)
            if prop_name in required_fields:
                entry['required'] = True
            suboptions[entry_snake] = entry
        if suboptions:
            return {'type': 'dict', 'options': suboptions}
        return {'type': 'dict'}

    # Array
    if schema_type == 'array':
        items_schema = schema.get('items', {})
        if '$ref' in items_schema:
            items_schema = resolve_refs_recursive(spec, items_schema)

        items_type = items_schema.get('type', 'string')
        if isinstance(items_type, list):
            non_null = [t for t in items_type if t != 'null']
            items_type = non_null[0] if non_null else 'string'

        if items_type in ('string', 'integer', 'boolean', 'number'):
            return {'type': 'list', 'elements': type_map.get(items_type, 'str')}

        # Array of objects
        if items_type == 'object' or 'properties' in items_schema:
            sub = openapi_type_to_ansible(items_schema, spec)
            result = {'type': 'list', 'elements': 'dict'}
            if 'options' in sub:
                result['options'] = sub['options']
            return result

        return {'type': 'list', 'elements': 'str'}

    return {'type': 'str'}


def schema_to_argument_spec(schema, spec, include_fields=None):
    """Convert an OpenAPI request schema to an Ansible argument_spec dict.

    Args:
        schema: The OpenAPI schema object (already resolved).
        spec: The full OpenAPI spec (for resolving nested $refs).
        include_fields: If given, only include these fields.

    Returns:
        Dict mapping snake_case field names to argument_spec entries.
    """
    if not schema or 'properties' not in schema:
        return {}

    result = {}
    required_fields = schema.get('required', [])

    for prop_name, prop_schema in schema.get('properties', {}).items():
        if prop_schema.get('readOnly'):
            continue

        snake_name = camel_to_snake(prop_name)
        if include_fields and snake_name not in include_fields:
            continue

        entry = openapi_type_to_ansible(prop_schema, spec)

        if prop_name in required_fields:
            entry['required'] = True

        desc = prop_schema.get('description', '')
        if desc:
            entry['description'] = desc

        result[snake_name] = entry

    return result


# ---------------------------------------------------------------------------
# Path analysis
# ---------------------------------------------------------------------------

def classify_path(path):
    """Classify a path as 'collection' or 'item'.

    Collection paths end with the resource name: /v2/org/{org}/carbide/vpc
    Item paths end with a path parameter: /v2/org/{org}/carbide/vpc/{vpcId}
    """
    last_segment = path.rstrip('/').split('/')[-1]
    if last_segment.startswith('{') and last_segment.endswith('}'):
        return 'item'
    return 'collection'


def extract_id_param(path):
    """Extract the ID parameter name from an item path."""
    last_segment = path.rstrip('/').split('/')[-1]
    if last_segment.startswith('{') and last_segment.endswith('}'):
        return last_segment[1:-1]
    return None


def detect_nested_module_name(path):
    """Detect the nested resource name from a path.

    e.g., /v2/org/{org}/carbide/allocation/{allocationId}/constraint -> allocation_constraint

    Returns None if the path is not a nested resource (or is a 'current' access pattern).
    """
    prefix = '/v2/org/{org}/carbide/'
    if not path.startswith(prefix):
        return None
    rest = path[len(prefix):]
    segments = rest.split('/')
    # Collect non-parameter segments
    resource_parts = [s for s in segments if not s.startswith('{')]

    # Skip 'current' access patterns - these are just alternative access
    # paths for the parent resource, not separate sub-resources
    resource_parts = [p for p in resource_parts if p != 'current']

    if len(resource_parts) >= 2:
        return '_'.join(camel_to_snake(p).replace('-', '_') for p in resource_parts)
    return None


def group_paths_by_tag(spec):
    """Group API paths by their tag, returning a dict of tag -> operations.

    Detects nested resources (e.g., allocation constraint) and splits them
    into separate groups with their own module names.
    """
    groups = {}
    paths = spec.get('paths', {})

    for path, path_item in paths.items():
        if path in SKIP_PATHS:
            continue

        # Get shared parameters (like org)
        shared_params = path_item.get('parameters', [])

        for method in ('get', 'post', 'patch', 'put', 'delete'):
            operation = path_item.get(method)
            if not operation:
                continue

            tags = operation.get('tags', [])
            if not tags:
                continue

            tag = tags[0]
            if tag in SKIP_TAGS:
                continue

            # Detect nested resources: paths like /allocation/{id}/constraint
            # should get their own module instead of being merged with the parent
            base_module = tag_to_module_name(tag)
            nested_name = detect_nested_module_name(path)

            if nested_name and nested_name != base_module:
                # This is a nested resource - create a separate group
                group_key = nested_name
                if group_key not in groups:
                    groups[group_key] = {
                        'operations': [],
                        'tag': tag,
                        'module_name': nested_name,
                    }
            else:
                group_key = tag
                if group_key not in groups:
                    groups[group_key] = {
                        'operations': [],
                        'tag': tag,
                        'module_name': base_module,
                    }

            groups[group_key]['operations'].append({
                'method': method.upper(),
                'path': path,
                'path_type': classify_path(path),
                'operation': operation,
                'id_param': extract_id_param(path),
                'shared_params': shared_params,
            })

    return groups


# ---------------------------------------------------------------------------
# Resource analysis
# ---------------------------------------------------------------------------

def analyze_resource(tag, group, spec):
    """Analyze a resource group and produce the resource config and argument_spec."""
    module_name = group['module_name']
    operations = group['operations']
    is_read_only = tag in READ_ONLY_TAGS

    resource_info = {
        'module_name': module_name,
        'tag': tag,
        'is_read_only': is_read_only,
        'collection_path': None,
        'item_path': None,
        'id_param': None,
        'has_create': False,
        'has_update': False,
        'has_delete': False,
        'has_list': False,
        'has_get': False,
        'create_schema': None,
        'update_schema': None,
        'delete_schema': None,
        'response_schema': None,
        'list_query_params': [],
        'description': '',
    }

    for op in operations:
        method = op['method']
        path = op['path']
        path_type = op['path_type']
        operation = op['operation']

        # Get tag description
        if not resource_info['description']:
            for t in spec.get('tags', []):
                if t.get('name') == tag:
                    desc = t.get('description', '')
                    # Take just the first paragraph
                    resource_info['description'] = desc.split('\n\n')[0].strip()
                    break

        if path_type == 'collection':
            resource_info['collection_path'] = path

            if method == 'GET':
                resource_info['has_list'] = True
                # Extract query parameters for _info module
                for param in operation.get('parameters', []):
                    if param.get('in') == 'query' and param.get('name') not in (
                        'pageNumber', 'pageSize', 'orderBy', 'includeRelation',
                    ):
                        resource_info['list_query_params'].append(param)

            elif method == 'POST':
                resource_info['has_create'] = True
                body = operation.get('requestBody', {})
                content = body.get('content', {}).get('application/json', {})
                schema = content.get('schema', {})
                if '$ref' in schema:
                    schema = resolve_refs_recursive(spec, schema)
                resource_info['create_schema'] = schema

        elif path_type == 'item':
            resource_info['item_path'] = path
            resource_info['id_param'] = op['id_param']

            if method == 'GET':
                resource_info['has_get'] = True
                # Get response schema
                resp = operation.get('responses', {}).get('200', {})
                content = resp.get('content', {}).get('application/json', {})
                schema = content.get('schema', {})
                if '$ref' in schema:
                    schema = resolve_refs_recursive(spec, schema)
                resource_info['response_schema'] = schema

            elif method == 'PATCH':
                resource_info['has_update'] = True
                body = operation.get('requestBody', {})
                content = body.get('content', {}).get('application/json', {})
                schema = content.get('schema', {})
                if '$ref' in schema:
                    schema = resolve_refs_recursive(spec, schema)
                resource_info['update_schema'] = schema

            elif method == 'DELETE':
                resource_info['has_delete'] = True
                body = operation.get('requestBody', {})
                if body:
                    content = body.get('content', {}).get('application/json', {})
                    schema = content.get('schema', {})
                    if '$ref' in schema:
                        schema = resolve_refs_recursive(spec, schema)
                    resource_info['delete_schema'] = schema

    # Also check for response schema from list endpoint if no item GET
    if not resource_info['response_schema'] and resource_info['has_list']:
        for op in operations:
            if op['method'] == 'GET' and op['path_type'] == 'collection':
                resp = op['operation'].get('responses', {}).get('200', {})
                content = resp.get('content', {}).get('application/json', {})
                schema = content.get('schema', {})
                if schema.get('type') == 'array':
                    items = schema.get('items', {})
                    if '$ref' in items:
                        items = resolve_refs_recursive(spec, items)
                    resource_info['response_schema'] = items
                elif '$ref' in schema:
                    resource_info['response_schema'] = resolve_refs_recursive(spec, schema)
                break

    # Handle special 'current' endpoints (service_account, infrastructure_provider, tenant, user)
    if not resource_info['item_path'] and not resource_info['collection_path']:
        for op in operations:
            if op['method'] == 'GET':
                resource_info['collection_path'] = op['path']
                resource_info['has_list'] = True
                resp = op['operation'].get('responses', {}).get('200', {})
                content = resp.get('content', {}).get('application/json', {})
                schema = content.get('schema', {})
                if '$ref' in schema:
                    schema = resolve_refs_recursive(spec, schema)
                resource_info['response_schema'] = schema
                break

    return resource_info


# ---------------------------------------------------------------------------
# Module code generation
# ---------------------------------------------------------------------------

def format_argument_spec(arg_spec, indent=0):
    """Format an argument_spec dict as Python source code."""
    lines = []
    prefix = '    ' * indent

    for name, entry in sorted(arg_spec.items()):
        parts = []
        for key in ('type', 'required', 'no_log', 'elements', 'choices', 'description'):
            if key in entry:
                val = entry[key]
                if key == 'description':
                    continue  # descriptions go in DOCUMENTATION, not argument_spec
                if isinstance(val, bool):
                    parts.append('%s=%s' % (key, val))
                elif isinstance(val, list):
                    parts.append('%s=%r' % (key, val))
                else:
                    parts.append("%s='%s'" % (key, val))

        if 'options' in entry:
            suboptions_str = format_argument_spec(entry['options'], indent + 1)
            parts.append('options=%s' % suboptions_str)

        line = '%s%s=dict(%s),' % (prefix, name, ', '.join(parts))
        lines.append(line)

    if indent == 0:
        return 'dict(\n%s\n)' % '\n'.join(lines)
    else:
        return 'dict(\n%s\n%s)' % ('\n'.join(lines), '    ' * (indent - 1))


def format_resource_config(config, indent=0):
    """Format a RESOURCE_CONFIG dict as Python source code."""
    lines = []
    prefix = '    '

    for key, value in config.items():
        if isinstance(value, list):
            if not value:
                lines.append("%s'%s': []," % (prefix, key))
            else:
                items_str = ', '.join("'%s'" % v for v in value)
                lines.append("%s'%s': [%s]," % (prefix, key, items_str))
        elif isinstance(value, bool):
            lines.append("%s'%s': %s," % (prefix, key, value))
        elif value is None:
            lines.append("%s'%s': None," % (prefix, key))
        else:
            lines.append("%s'%s': '%s'," % (prefix, key, value))

    return '{\n%s\n}' % '\n'.join(lines)


def generate_doc_string(module_name, tag, description, arg_spec, is_info=False, is_batch=False):
    """Generate DOCUMENTATION YAML string."""
    mod_type = 'info' if is_info else ('batch' if is_batch else 'CRUD')
    short_desc = 'Retrieve %s information' % tag if is_info else 'Manage %s resources' % tag
    if is_batch:
        short_desc = 'Batch create %s resources' % tag

    full_module_name = 'nvidia.bare_metal.%s' % module_name

    doc = {
        'module': full_module_name,
        'short_description': short_desc,
        'description': [description or short_desc],
        'version_added': '1.0.0',
        'author': 'NVIDIA Bare Metal Manager Dev Team',
        'extends_documentation_fragment': ['nvidia.bare_metal.auth'],
        'options': {},
    }

    for name, entry in sorted(arg_spec.items()):
        if name in ('api_url', 'api_token', 'org'):
            continue
        opt = {'type': entry.get('type', 'str')}
        desc = entry.get('description', '')
        if desc:
            opt['description'] = [desc]
        else:
            opt['description'] = ['%s parameter.' % name]
        if entry.get('required'):
            opt['required'] = True
        if 'choices' in entry:
            opt['choices'] = entry['choices']
        if 'elements' in entry:
            opt['elements'] = entry['elements']
        if 'options' in entry:
            sub_opts = {}
            for sub_name, sub_entry in sorted(entry['options'].items()):
                sub_opt = {'type': sub_entry.get('type', 'str')}
                sub_desc = sub_entry.get('description', '')
                sub_opt['description'] = [sub_desc] if sub_desc else ['%s parameter.' % sub_name]
                if sub_entry.get('required'):
                    sub_opt['required'] = True
                if 'choices' in sub_entry:
                    sub_opt['choices'] = sub_entry['choices']
                if 'elements' in sub_entry:
                    sub_opt['elements'] = sub_entry['elements']
                sub_opts[sub_name] = sub_opt
            opt['suboptions'] = sub_opts
        doc['options'][name] = opt

    # Use block scalar style for cleaner output
    return yaml.dump(doc, default_flow_style=False, sort_keys=False, width=120)


def generate_examples(module_name, tag, is_info=False, is_batch=False, has_create=True):
    """Generate EXAMPLES string."""
    lines = []
    full_name = 'nvidia.bare_metal.%s' % module_name

    if is_info:
        lines.append('- name: List all %s resources' % tag)
        lines.append('  %s:' % full_name)
        lines.append('    api_url: "{{ api_url }}"')
        lines.append('    api_token: "{{ api_token }}"')
        lines.append('    org: "{{ org }}"')
        lines.append('')
        lines.append('- name: Get a specific %s by ID' % tag)
        lines.append('  %s:' % full_name)
        lines.append('    api_url: "{{ api_url }}"')
        lines.append('    api_token: "{{ api_token }}"')
        lines.append('    org: "{{ org }}"')
        lines.append('    id: "{{ resource_id }}"')
    elif is_batch:
        lines.append('- name: Batch create %s resources' % tag)
        lines.append('  %s:' % full_name)
        lines.append('    api_url: "{{ api_url }}"')
        lines.append('    api_token: "{{ api_token }}"')
        lines.append('    org: "{{ org }}"')
    else:
        if has_create:
            lines.append('- name: Create a %s' % tag)
            lines.append('  %s:' % full_name)
            lines.append('    api_url: "{{ api_url }}"')
            lines.append('    api_token: "{{ api_token }}"')
            lines.append('    org: "{{ org }}"')
            lines.append('    state: present')
            lines.append('    name: "my-%s"' % module_name.replace('_', '-'))
            lines.append('')

        lines.append('- name: Delete a %s' % tag)
        lines.append('  %s:' % full_name)
        lines.append('    api_url: "{{ api_url }}"')
        lines.append('    api_token: "{{ api_token }}"')
        lines.append('    org: "{{ org }}"')
        lines.append('    state: absent')
        if has_create:
            lines.append('    name: "my-%s"' % module_name.replace('_', '-'))
        else:
            lines.append('    id: "{{ resource_id }}"')

    return '\n'.join(lines)


def generate_return_doc(response_schema, spec, is_info=False, is_batch=False):
    """Generate RETURN documentation string."""
    if is_info:
        return textwrap.dedent('''\
            resources:
                description: List of resources.
                type: list
                returned: when no id is specified
                elements: dict
            resource:
                description: Single resource details.
                type: dict
                returned: when id is specified''')
    if is_batch:
        return textwrap.dedent('''\
            resources:
                description: List of created resources.
                type: list
                returned: always
                elements: dict''')
    return textwrap.dedent('''\
        resource:
            description: The resource details.
            type: dict
            returned: when state is present''')


def generate_crud_module(resource_info, spec, overrides):
    """Generate the Python source for a CRUD module."""
    module_name = resource_info['module_name']
    tag = resource_info['tag']

    # Build argument_spec from create + update schemas
    arg_spec = {}

    # ID parameter
    arg_spec['id'] = {'type': 'str', 'description': 'ID of the resource. Used for lookup.'}

    # State parameter
    arg_spec['state'] = {
        'type': 'str',
        'choices': ['present', 'absent'],
        'description': 'Desired state of the resource.',
    }

    # Wait parameters
    arg_spec['wait'] = {'type': 'bool', 'description': 'Wait for the resource to reach the desired state.'}
    arg_spec['wait_timeout'] = {'type': 'int', 'description': 'Timeout in seconds for wait operations.'}

    # Merge create and update fields
    create_fields = []
    update_fields = []

    if resource_info['create_schema']:
        create_spec = schema_to_argument_spec(resource_info['create_schema'], spec)
        for k, v in create_spec.items():
            if k not in arg_spec:
                # Drop 'required' from create-only fields — they're not needed
                # for state=absent. Validation happens in CrudResource.run().
                v = dict(v)
                v.pop('required', None)
                arg_spec[k] = v
            create_fields.append(k)

    if resource_info['update_schema']:
        update_spec = schema_to_argument_spec(resource_info['update_schema'], spec)
        for k, v in update_spec.items():
            if k not in arg_spec:
                v = dict(v)
                v.pop('required', None)
                arg_spec[k] = v
            update_fields.append(k)

    # Handle delete body fields
    delete_body_fields = overrides.get('delete_body_fields', [])
    if resource_info.get('delete_schema'):
        delete_spec = schema_to_argument_spec(resource_info['delete_schema'], spec)
        for k, v in delete_spec.items():
            if k not in arg_spec:
                v = dict(v)
                v.pop('required', None)
                arg_spec[k] = v
            if k not in delete_body_fields:
                delete_body_fields.append(k)

    # Determine name_field: use override if present, otherwise 'name' if the
    # resource schema actually has a 'name' property, else None.
    name_field_override = overrides.get('name_field', 'UNSET')
    if name_field_override != 'UNSET':
        # Explicit override (could be None to disable name-based lookup)
        name_field = name_field_override
    elif 'name' in create_fields or 'name' in update_fields:
        name_field = 'name'
    else:
        name_field = None

    # Add scope fields and parent path params to argument_spec if not already present.
    scope_fields = overrides.get('scope_fields', [])
    for field in scope_fields:
        if field not in arg_spec:
            arg_spec[field] = {
                'type': 'str',
                'description': 'Scope filter: %s.' % field,
            }

    # Detect additional path params (e.g., allocationId in nested paths)
    # and add them to arg_spec so users can provide them.
    collection_path = resource_info['collection_path'] or ''
    item_path = resource_info['item_path'] or ''
    for path in (collection_path, item_path):
        for match in _PATH_PARAM_RE.findall(path):
            if match == 'org':
                continue
            snake_param = camel_to_snake(match)
            if snake_param not in arg_spec:
                arg_spec[snake_param] = {
                    'type': 'str',
                    'description': 'ID path parameter: %s.' % snake_param,
                }

    # Detect version field for optimistic concurrency control.
    # If the update schema has a required 'version' field, the PATCH must
    # include the current version from the existing resource.
    version_field = None
    if resource_info.get('update_schema'):
        update_required = resource_info['update_schema'].get('required', [])
        if 'version' in update_required:
            version_field = 'version'

    # Build resource config
    resource_config = {
        'resource_path': resource_info['collection_path'] or '',
        'resource_item_path': resource_info['item_path'] or '',
        'id_param': resource_info['id_param'] or 'id',
        'name_field': name_field,
        'create_schema_fields': create_fields,
        'update_schema_fields': update_fields,
        'scope_fields': scope_fields,
        'ready_statuses': overrides.get('ready_statuses', ['Ready']),
        'error_statuses': overrides.get('error_statuses', ['Error']),
        'no_create': overrides.get('no_create', False),
        'delete_body_fields': delete_body_fields,
        'version_field': version_field,
    }

    # Format as Python
    has_create = resource_info['has_create'] and not overrides.get('no_create', False)

    arg_spec_str = format_argument_spec(arg_spec)
    resource_config_str = format_resource_config(resource_config)
    doc_str = generate_doc_string(module_name, tag, resource_info['description'], arg_spec)
    examples_str = generate_examples(module_name, tag, has_create=has_create)
    return_str = generate_return_doc(resource_info.get('response_schema'), spec)

    code = '''\
#!/usr/bin/python
# -*- coding: utf-8 -*-
# This file is auto-generated by scripts/generate.py. Do not edit manually.

# SPDX-FileCopyrightText: Copyright (c) 2026 Fabien Dupont
# SPDX-License-Identifier: Apache-2.0

from __future__ import absolute_import, division, print_function
__metaclass__ = type

DOCUMENTATION = r\'\'\'
---
%s\'\'\'

EXAMPLES = r\'\'\'
---
%s
\'\'\'

RETURN = r\'\'\'
---
%s
\'\'\'

from ansible.module_utils.basic import AnsibleModule
from ansible_collections.nvidia.bare_metal.plugins.module_utils.common import get_auth_argument_spec
from ansible_collections.nvidia.bare_metal.plugins.module_utils.resource import CrudResource


ARGUMENT_SPEC = %s

RESOURCE_CONFIG = %s


def main():
    auth_spec = get_auth_argument_spec()
    auth_spec.update(ARGUMENT_SPEC)
    module = AnsibleModule(argument_spec=auth_spec, supports_check_mode=True)
    CrudResource(module, RESOURCE_CONFIG).run()


if __name__ == "__main__":
    main()
''' % (doc_str, examples_str, return_str, arg_spec_str, resource_config_str)

    return code


def generate_info_module(resource_info, spec, overrides):
    """Generate the Python source for an _info module."""
    module_name = resource_info['module_name'] + '_info'
    tag = resource_info['tag']

    arg_spec = {}

    # ID parameter for single lookup
    if resource_info['item_path']:
        arg_spec['id'] = {'type': 'str', 'description': 'ID of the resource to retrieve.'}

    # Filter parameters from list query params
    filter_fields = []
    for param in resource_info.get('list_query_params', []):
        param_name = param.get('name', '')
        snake_name = camel_to_snake(param_name)
        param_schema = param.get('schema', {})

        entry = openapi_type_to_ansible(param_schema, spec)
        desc = param.get('description', '')
        if desc:
            entry['description'] = desc

        arg_spec[snake_name] = entry
        filter_fields.append(snake_name)

    # Detect path params and add them to arg_spec so users can provide them
    collection_path = resource_info['collection_path'] or ''
    item_path = resource_info['item_path'] or ''
    for path in (collection_path, item_path):
        for match in _PATH_PARAM_RE.findall(path):
            if match == 'org':
                continue
            snake_param = camel_to_snake(match)
            if snake_param not in arg_spec:
                arg_spec[snake_param] = {
                    'type': 'str',
                    'description': 'ID path parameter: %s.' % snake_param,
                }

    # Build resource config
    resource_config = {
        'resource_path': collection_path,
        'resource_item_path': item_path,
        'id_param': resource_info['id_param'] or 'id',
        'filter_fields': filter_fields,
    }

    arg_spec_str = format_argument_spec(arg_spec)
    resource_config_str = format_resource_config(resource_config)
    doc_str = generate_doc_string(module_name, tag, resource_info['description'], arg_spec, is_info=True)
    examples_str = generate_examples(module_name, tag, is_info=True)
    return_str = generate_return_doc(resource_info.get('response_schema'), spec, is_info=True)

    code = '''\
#!/usr/bin/python
# -*- coding: utf-8 -*-
# This file is auto-generated by scripts/generate.py. Do not edit manually.

# SPDX-FileCopyrightText: Copyright (c) 2026 Fabien Dupont
# SPDX-License-Identifier: Apache-2.0

from __future__ import absolute_import, division, print_function
__metaclass__ = type

DOCUMENTATION = r\'\'\'
---
%s\'\'\'

EXAMPLES = r\'\'\'
---
%s
\'\'\'

RETURN = r\'\'\'
---
%s
\'\'\'

from ansible.module_utils.basic import AnsibleModule
from ansible_collections.nvidia.bare_metal.plugins.module_utils.common import get_auth_argument_spec
from ansible_collections.nvidia.bare_metal.plugins.module_utils.resource import InfoResource


ARGUMENT_SPEC = %s

RESOURCE_CONFIG = %s


def main():
    auth_spec = get_auth_argument_spec()
    auth_spec.update(ARGUMENT_SPEC)
    module = AnsibleModule(argument_spec=auth_spec, supports_check_mode=True)
    InfoResource(module, RESOURCE_CONFIG).run()


if __name__ == "__main__":
    main()
''' % (doc_str, examples_str, return_str, arg_spec_str, resource_config_str)

    return code


def generate_batch_module(resource_info, spec, overrides):
    """Generate the Python source for a batch-create module."""
    module_name = resource_info['module_name']
    tag = resource_info['tag']

    arg_spec = {}
    create_fields = []

    if resource_info['create_schema']:
        create_spec = schema_to_argument_spec(resource_info['create_schema'], spec)
        for k, v in create_spec.items():
            arg_spec[k] = v
            create_fields.append(k)

    resource_config = {
        'resource_path': resource_info['collection_path'] or '',
        'create_schema_fields': create_fields,
    }

    arg_spec_str = format_argument_spec(arg_spec)
    resource_config_str = format_resource_config(resource_config)
    doc_str = generate_doc_string(module_name, tag, resource_info['description'], arg_spec, is_batch=True)
    examples_str = generate_examples(module_name, tag, is_batch=True)
    return_str = generate_return_doc(None, spec, is_batch=True)

    code = '''\
#!/usr/bin/python
# -*- coding: utf-8 -*-
# This file is auto-generated by scripts/generate.py. Do not edit manually.

# SPDX-FileCopyrightText: Copyright (c) 2026 Fabien Dupont
# SPDX-License-Identifier: Apache-2.0

from __future__ import absolute_import, division, print_function
__metaclass__ = type

DOCUMENTATION = r\'\'\'
---
%s\'\'\'

EXAMPLES = r\'\'\'
---
%s
\'\'\'

RETURN = r\'\'\'
---
%s
\'\'\'

from ansible.module_utils.basic import AnsibleModule
from ansible_collections.nvidia.bare_metal.plugins.module_utils.common import get_auth_argument_spec
from ansible_collections.nvidia.bare_metal.plugins.module_utils.resource import BatchResource


ARGUMENT_SPEC = %s

RESOURCE_CONFIG = %s


def main():
    auth_spec = get_auth_argument_spec()
    auth_spec.update(ARGUMENT_SPEC)
    module = AnsibleModule(argument_spec=auth_spec, supports_check_mode=True)
    BatchResource(module, RESOURCE_CONFIG).run()


if __name__ == "__main__":
    main()
''' % (doc_str, examples_str, return_str, arg_spec_str, resource_config_str)

    return code


def generate_action_module(module_name, action_config, spec):
    """Generate the Python source for an action module (validation, power, firmware)."""
    tag = action_config['tag']
    method = action_config['method']
    collection_path = action_config['collection_path']
    item_path = action_config.get('item_path')

    arg_spec = {}
    body_fields = []
    query_fields = []

    # Always add id param (optional, for single-resource targeting)
    arg_spec['id'] = {'type': 'str', 'description': 'ID of the resource. When provided, targets a single resource.'}

    if method == 'GET':
        # For GET actions, extract query params from response schema (no request body)
        pass
    else:
        # For PATCH/POST actions, extract fields from the item request schema
        item_ref = action_config.get('item_request_schema_ref')
        if item_ref:
            item_schema = resolve_ref(spec, item_ref)
            item_schema = resolve_refs_recursive(spec, item_schema)
            item_spec = schema_to_argument_spec(item_schema, spec)
            for k, v in item_spec.items():
                if k not in arg_spec:
                    v = dict(v)
                    v.pop('required', None)
                    arg_spec[k] = v
                body_fields.append(k)

        # Also check collection request schema for batch-specific fields
        collection_ref = action_config.get('collection_request_schema_ref')
        if collection_ref:
            coll_schema = resolve_ref(spec, collection_ref)
            coll_schema = resolve_refs_recursive(spec, coll_schema)
            coll_spec = schema_to_argument_spec(coll_schema, spec)
            for k, v in coll_spec.items():
                if k not in arg_spec:
                    v = dict(v)
                    v.pop('required', None)
                    arg_spec[k] = v
                if k not in body_fields:
                    body_fields.append(k)

    # Build resource config
    resource_config = {
        'resource_path': collection_path,
        'resource_item_path': item_path or '',
        'method': method,
        'body_fields': body_fields,
        'query_fields': query_fields,
    }

    # Get tag description
    description = ''
    for t in spec.get('tags', []):
        if t.get('name') == tag:
            description = t.get('description', '').split('\n\n')[0].strip()
            break

    action_type = module_name.split('_')[-1]  # validation, power, firmware
    short_desc = '%s %s for %s resources' % (
        action_type.capitalize(),
        'check' if method == 'GET' else 'action',
        tag,
    )

    arg_spec_str = format_argument_spec(arg_spec)
    resource_config_str = format_resource_config(resource_config)
    doc_str = generate_doc_string(module_name, tag, description or short_desc, arg_spec)
    return_str = textwrap.dedent('''\
        result:
            description: The action result.
            type: dict
            returned: always''')

    # Generate examples
    full_name = 'nvidia.bare_metal.%s' % module_name
    examples_lines = []
    examples_lines.append('- name: Run %s on all %s resources' % (action_type, tag.lower()))
    examples_lines.append('  %s:' % full_name)
    examples_lines.append('    api_url: "{{ api_url }}"')
    examples_lines.append('    api_token: "{{ api_token }}"')
    examples_lines.append('    org: "{{ org }}"')
    examples_lines.append('')
    examples_lines.append('- name: Run %s on a specific %s' % (action_type, tag.lower()))
    examples_lines.append('  %s:' % full_name)
    examples_lines.append('    api_url: "{{ api_url }}"')
    examples_lines.append('    api_token: "{{ api_token }}"')
    examples_lines.append('    org: "{{ org }}"')
    examples_lines.append('    id: "{{ resource_id }}"')
    examples_str = '\n'.join(examples_lines)

    supports_check = 'True' if method != 'GET' else 'True'

    code = '''\
#!/usr/bin/python
# -*- coding: utf-8 -*-
# This file is auto-generated by scripts/generate.py. Do not edit manually.

# SPDX-FileCopyrightText: Copyright (c) 2026 Fabien Dupont
# SPDX-License-Identifier: Apache-2.0

from __future__ import absolute_import, division, print_function
__metaclass__ = type

DOCUMENTATION = r\'\'\'
---
%s\'\'\'

EXAMPLES = r\'\'\'
---
%s
\'\'\'

RETURN = r\'\'\'
---
%s
\'\'\'

from ansible.module_utils.basic import AnsibleModule
from ansible_collections.nvidia.bare_metal.plugins.module_utils.common import get_auth_argument_spec
from ansible_collections.nvidia.bare_metal.plugins.module_utils.resource import ActionResource


ARGUMENT_SPEC = %s

RESOURCE_CONFIG = %s


def main():
    auth_spec = get_auth_argument_spec()
    auth_spec.update(ARGUMENT_SPEC)
    module = AnsibleModule(argument_spec=auth_spec, supports_check_mode=%s)
    ActionResource(module, RESOURCE_CONFIG).run()


if __name__ == "__main__":
    main()
''' % (doc_str, examples_str, return_str, arg_spec_str, resource_config_str, supports_check)

    return code


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(description='Generate Ansible modules from OpenAPI spec')
    parser.add_argument('--spec', required=True, help='Path to OpenAPI spec.yaml')
    parser.add_argument('--output', required=True, help='Output directory for generated modules')
    parser.add_argument('--dry-run', action='store_true', help='Print module names without writing files')
    args = parser.parse_args()

    # Load spec
    with open(args.spec, 'r') as f:
        spec = yaml.safe_load(f)

    # Track spec version
    spec_version = spec.get('info', {}).get('version', 'unknown')
    print('Spec version: %s' % spec_version)

    # Update spec_version in galaxy.yml
    if not args.dry_run:
        galaxy_path = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), 'galaxy.yml')
        if os.path.exists(galaxy_path):
            with open(galaxy_path, 'r') as f:
                galaxy_content = f.read()
            # Update or add spec_version field
            if 'spec_version:' in galaxy_content:
                galaxy_content = re.sub(
                    r'spec_version:.*',
                    'spec_version: "%s"' % spec_version,
                    galaxy_content,
                )
            else:
                galaxy_content = galaxy_content.rstrip() + '\nspec_version: "%s"\n' % spec_version
            with open(galaxy_path, 'w') as f:
                f.write(galaxy_content)
            print('Updated galaxy.yml spec_version to %s' % spec_version)

    # Group paths by tag
    groups = group_paths_by_tag(spec)

    # Ensure output directory exists
    if not args.dry_run:
        os.makedirs(args.output, exist_ok=True)

    generated = []

    for tag, group in sorted(groups.items()):
        module_name = group['module_name']
        overrides = RESOURCE_OVERRIDES.get(module_name, {})
        is_read_only = tag in READ_ONLY_TAGS

        # Analyze the resource
        resource_info = analyze_resource(tag, group, spec)

        module_type = overrides.get('module_type', None)

        if module_type == 'batch':
            # Batch-create-only module
            code = generate_batch_module(resource_info, spec, overrides)
            filename = '%s.py' % module_name
            if args.dry_run:
                print('  [batch] %s' % filename)
            else:
                filepath = os.path.join(args.output, filename)
                with open(filepath, 'w') as f:
                    f.write(code)
                print('  Generated %s' % filepath)
            generated.append(filename)

        elif is_read_only:
            # Read-only: only generate _info module
            code = generate_info_module(resource_info, spec, overrides)
            filename = '%s_info.py' % module_name
            if args.dry_run:
                print('  [info-only] %s' % filename)
            else:
                filepath = os.path.join(args.output, filename)
                with open(filepath, 'w') as f:
                    f.write(code)
                print('  Generated %s' % filepath)
            generated.append(filename)

        else:
            # CRUD resource: generate both module.py and module_info.py
            if resource_info['has_create'] or resource_info['has_update'] or resource_info['has_delete']:
                code = generate_crud_module(resource_info, spec, overrides)
                filename = '%s.py' % module_name
                if args.dry_run:
                    print('  [crud] %s' % filename)
                else:
                    filepath = os.path.join(args.output, filename)
                    with open(filepath, 'w') as f:
                        f.write(code)
                    print('  Generated %s' % filepath)
                generated.append(filename)

            if resource_info['has_list'] or resource_info['has_get']:
                code = generate_info_module(resource_info, spec, overrides)
                filename = '%s_info.py' % module_name
                if args.dry_run:
                    print('  [info] %s' % filename)
                else:
                    filepath = os.path.join(args.output, filename)
                    with open(filepath, 'w') as f:
                        f.write(code)
                    print('  Generated %s' % filepath)
                generated.append(filename)

    # Generate batch modules (special cases not covered by tag grouping)
    for batch_module_name, batch_config in BATCH_MODULES.items():
        batch_path = batch_config['path']
        batch_tag = batch_config['tag']
        schema_ref = batch_config['schema_ref']

        # Resolve the batch schema
        batch_schema = resolve_ref(spec, schema_ref)
        batch_schema = resolve_refs_recursive(spec, batch_schema)

        # Get tag description
        batch_desc = ''
        for t in spec.get('tags', []):
            if t.get('name') == batch_tag:
                batch_desc = t.get('description', '').split('\n\n')[0].strip()
                break

        batch_resource_info = {
            'module_name': batch_module_name,
            'tag': batch_tag,
            'collection_path': batch_path,
            'create_schema': batch_schema,
            'description': 'Batch create %s' % batch_tag,
        }

        overrides = RESOURCE_OVERRIDES.get(batch_module_name, {})
        code = generate_batch_module(batch_resource_info, spec, overrides)
        filename = '%s.py' % batch_module_name
        if args.dry_run:
            print('  [batch] %s' % filename)
        else:
            filepath = os.path.join(args.output, filename)
            with open(filepath, 'w') as f:
                f.write(code)
            print('  Generated %s' % filepath)
        generated.append(filename)

    # Generate info-only modules for mis-tagged or special endpoints
    for info_module_name, info_config in INFO_ONLY_MODULES.items():
        info_tag = info_config['tag']
        collection_path = info_config['collection_path']
        response_ref = info_config.get('response_schema_ref')

        info_desc = ''
        for t in spec.get('tags', []):
            if t.get('name') == info_tag:
                info_desc = t.get('description', '').split('\n\n')[0].strip()
                break

        response_schema = None
        if response_ref:
            response_schema = resolve_ref(spec, response_ref)
            response_schema = resolve_refs_recursive(spec, response_schema)

        info_resource_info = {
            'module_name': info_module_name,
            'tag': info_tag,
            'collection_path': collection_path,
            'item_path': info_config.get('item_path'),
            'id_param': info_config.get('id_param'),
            'has_list': True,
            'has_get': bool(info_config.get('item_path')),
            'list_query_params': [],
            'response_schema': response_schema,
            'description': info_desc or '%s operations' % info_tag,
        }

        overrides = RESOURCE_OVERRIDES.get(info_module_name, {})
        code = generate_info_module(info_resource_info, spec, overrides)
        filename = '%s_info.py' % info_module_name
        if args.dry_run:
            print('  [info-special] %s' % filename)
        else:
            filepath = os.path.join(args.output, filename)
            with open(filepath, 'w') as f:
                f.write(code)
            print('  Generated %s' % filepath)
        generated.append(filename)

    # Generate action modules (validation, power, firmware)
    for action_module_name, action_config in ACTION_MODULES.items():
        code = generate_action_module(action_module_name, action_config, spec)
        filename = '%s.py' % action_module_name
        if args.dry_run:
            print('  [action] %s' % filename)
        else:
            filepath = os.path.join(args.output, filename)
            with open(filepath, 'w') as f:
                f.write(code)
            print('  Generated %s' % filepath)
        generated.append(filename)

    print('\nGenerated %d module files.' % len(generated))
    return generated


if __name__ == '__main__':
    main()
