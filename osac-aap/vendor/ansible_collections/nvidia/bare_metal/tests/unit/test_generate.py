# SPDX-FileCopyrightText: Copyright (c) 2026 Fabien Dupont
# SPDX-License-Identifier: Apache-2.0

import os
import sys
import tempfile

import pytest
import yaml

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', '..', 'scripts'))

from generate import (
    camel_to_snake,
    tag_to_module_name,
    resolve_ref,
    resolve_refs_recursive,
    openapi_type_to_ansible,
    schema_to_argument_spec,
    classify_path,
    extract_id_param,
    detect_nested_module_name,
    main as generate_main,
)


class TestTagToModuleName:
    def test_simple(self):
        assert tag_to_module_name('VPC') == 'vpc'

    def test_two_words(self):
        assert tag_to_module_name('SSH Key Group') == 'ssh_key_group'

    def test_ip_block(self):
        assert tag_to_module_name('IP Block') == 'ip_block'

    def test_infiniband(self):
        assert tag_to_module_name('InfiniBand Partition') == 'infiniband_partition'

    def test_operating_system(self):
        assert tag_to_module_name('Operating System') == 'operating_system'

    def test_instance_type(self):
        assert tag_to_module_name('Instance Type') == 'instance_type'

    def test_unmapped_tag(self):
        # Tags not in TAG_TO_MODULE fall back to auto-conversion
        result = tag_to_module_name('Site')
        assert result == 'site'

    def test_allocation(self):
        result = tag_to_module_name('Allocation')
        assert result == 'allocation'


class TestResolveRef:
    def test_resolve(self):
        spec = {
            'components': {
                'schemas': {
                    'VPC': {'type': 'object', 'properties': {'name': {'type': 'string'}}}
                }
            }
        }
        result = resolve_ref(spec, '#/components/schemas/VPC')
        assert result['type'] == 'object'
        assert 'name' in result['properties']

    def test_invalid_ref(self):
        spec = {'components': {'schemas': {}}}
        result = resolve_ref(spec, '#/components/schemas/Missing')
        assert result == {}


class TestResolveRefsRecursive:
    def test_nested_refs(self):
        spec = {
            'components': {
                'schemas': {
                    'Inner': {'type': 'string', 'enum': ['A', 'B']},
                    'Outer': {
                        'type': 'object',
                        'properties': {
                            'status': {'$ref': '#/components/schemas/Inner'},
                        },
                    },
                },
            },
        }
        result = resolve_refs_recursive(spec, spec['components']['schemas']['Outer'])
        assert result['properties']['status']['type'] == 'string'
        assert result['properties']['status']['enum'] == ['A', 'B']


class TestOpenApiTypeToAnsible:
    def test_string(self):
        assert openapi_type_to_ansible({'type': 'string'}, {}) == {'type': 'str'}

    def test_integer(self):
        assert openapi_type_to_ansible({'type': 'integer'}, {}) == {'type': 'int'}

    def test_boolean(self):
        assert openapi_type_to_ansible({'type': 'boolean'}, {}) == {'type': 'bool'}

    def test_string_enum(self):
        result = openapi_type_to_ansible({'type': 'string', 'enum': ['A', 'B']}, {})
        assert result == {'type': 'str', 'choices': ['A', 'B']}

    def test_nullable_string(self):
        result = openapi_type_to_ansible({'type': ['string', 'null']}, {})
        assert result == {'type': 'str'}

    def test_array_of_strings(self):
        result = openapi_type_to_ansible(
            {'type': 'array', 'items': {'type': 'string'}}, {}
        )
        assert result == {'type': 'list', 'elements': 'str'}

    def test_array_of_uuids(self):
        result = openapi_type_to_ansible(
            {'type': 'array', 'items': {'type': 'string', 'format': 'uuid'}}, {}
        )
        assert result == {'type': 'list', 'elements': 'str'}

    def test_object_with_additional_properties(self):
        result = openapi_type_to_ansible(
            {'type': 'object', 'additionalProperties': {'type': 'string'}}, {}
        )
        assert result == {'type': 'dict'}

    def test_object_with_properties(self):
        schema = {
            'type': 'object',
            'properties': {
                'subnetId': {'type': 'string'},
                'isPhysical': {'type': 'boolean'},
            },
        }
        result = openapi_type_to_ansible(schema, {})
        assert result['type'] == 'dict'
        assert 'options' in result
        assert 'subnet_id' in result['options']
        assert result['options']['subnet_id'] == {'type': 'str'}
        assert result['options']['is_physical'] == {'type': 'bool'}

    def test_empty_schema(self):
        assert openapi_type_to_ansible({}, {}) == {'type': 'str'}
        assert openapi_type_to_ansible(None, {}) == {'type': 'str'}


class TestSchemaToArgumentSpec:
    def test_basic_schema(self):
        schema = {
            'type': 'object',
            'properties': {
                'name': {'type': 'string', 'description': 'Name'},
                'siteId': {'type': 'string', 'format': 'uuid'},
                'id': {'type': 'string', 'readOnly': True},
                'status': {'type': 'string', 'readOnly': True},
            },
            'required': ['name', 'siteId'],
        }
        result = schema_to_argument_spec(schema, {})

        assert 'name' in result
        assert result['name']['required'] is True
        assert 'site_id' in result
        assert result['site_id']['required'] is True
        # readOnly fields excluded
        assert 'id' not in result
        assert 'status' not in result

    def test_include_fields(self):
        schema = {
            'type': 'object',
            'properties': {
                'name': {'type': 'string'},
                'description': {'type': 'string'},
                'siteId': {'type': 'string'},
            },
        }
        result = schema_to_argument_spec(schema, {}, include_fields=['name', 'description'])
        assert 'name' in result
        assert 'description' in result
        assert 'site_id' not in result


class TestClassifyPath:
    def test_collection(self):
        assert classify_path('/v2/org/{org}/carbide/vpc') == 'collection'

    def test_item(self):
        assert classify_path('/v2/org/{org}/carbide/vpc/{vpcId}') == 'item'

    def test_nested_collection(self):
        assert classify_path('/v2/org/{org}/carbide/allocation/{allocationId}/constraint') == 'collection'

    def test_nested_item(self):
        assert classify_path('/v2/org/{org}/carbide/allocation/{allocationId}/constraint/{constraintId}') == 'item'


class TestExtractIdParam:
    def test_item_path(self):
        assert extract_id_param('/v2/org/{org}/carbide/vpc/{vpcId}') == 'vpcId'

    def test_collection_path(self):
        assert extract_id_param('/v2/org/{org}/carbide/vpc') is None


class TestDetectNestedModuleName:
    def test_not_nested(self):
        assert detect_nested_module_name('/v2/org/{org}/carbide/vpc') is None

    def test_nested_constraint(self):
        assert detect_nested_module_name(
            '/v2/org/{org}/carbide/allocation/{allocationId}/constraint'
        ) == 'allocation_constraint'

    def test_instance_type(self):
        assert detect_nested_module_name(
            '/v2/org/{org}/carbide/instance/type'
        ) == 'instance_type'

    def test_current_skipped(self):
        assert detect_nested_module_name(
            '/v2/org/{org}/carbide/infrastructure-provider/current'
        ) is None

    def test_non_carbide_path(self):
        assert detect_nested_module_name('/v2/org/{org}/forge/something') is None


class TestFullGeneration:
    """Integration test: run the generator against the real spec."""

    @pytest.fixture
    def spec_path(self):
        path = os.path.join(
            os.path.dirname(__file__), '..', '..', '..',
            'bare-metal-manager-rest', 'openapi', 'spec.yaml',
        )
        if not os.path.exists(path):
            pytest.skip('OpenAPI spec not found')
        return path

    def test_generates_expected_modules(self, spec_path):
        with tempfile.TemporaryDirectory() as tmpdir:
            sys.argv = ['generate.py', '--spec', spec_path, '--output', tmpdir]
            generated = generate_main()

            # Check key modules exist
            assert 'vpc.py' in generated
            assert 'vpc_info.py' in generated
            assert 'instance.py' in generated
            assert 'instance_info.py' in generated
            assert 'instance_batch.py' in generated
            assert 'machine.py' in generated
            assert 'machine_info.py' in generated
            assert 'allocation_constraint.py' in generated
            assert 'allocation_constraint_info.py' in generated
            assert 'machine_capability_info.py' in generated

            # Read-only modules
            assert 'service_account_info.py' in generated
            assert 'infrastructure_provider_info.py' in generated
            assert 'tenant_info.py' in generated
            assert 'user_info.py' in generated
            assert 'metadata_info.py' in generated
            assert 'audit_info.py' in generated
            assert 'rack_info.py' in generated
            assert 'sku_info.py' in generated

            # All files should be valid Python
            for filename in generated:
                filepath = os.path.join(tmpdir, filename)
                assert os.path.exists(filepath), 'Missing file: %s' % filename
                with open(filepath) as f:
                    source = f.read()
                compile(source, filepath, 'exec')

    def test_vpc_module_structure(self, spec_path):
        with tempfile.TemporaryDirectory() as tmpdir:
            sys.argv = ['generate.py', '--spec', spec_path, '--output', tmpdir]
            generate_main()

            with open(os.path.join(tmpdir, 'vpc.py')) as f:
                source = f.read()

            assert 'DOCUMENTATION' in source
            assert 'EXAMPLES' in source
            assert 'RETURN' in source
            assert 'ARGUMENT_SPEC' in source
            assert 'RESOURCE_CONFIG' in source
            assert 'CrudResource' in source
            assert "resource_path': '/v2/org/{org}/carbide/vpc'" in source
            assert "'id_param': 'vpcId'" in source

    def test_machine_no_create(self, spec_path):
        with tempfile.TemporaryDirectory() as tmpdir:
            sys.argv = ['generate.py', '--spec', spec_path, '--output', tmpdir]
            generate_main()

            with open(os.path.join(tmpdir, 'machine.py')) as f:
                source = f.read()

            assert "'no_create': True" in source
            assert "'name_field': None" in source

    def test_instance_batch_module(self, spec_path):
        with tempfile.TemporaryDirectory() as tmpdir:
            sys.argv = ['generate.py', '--spec', spec_path, '--output', tmpdir]
            generate_main()

            with open(os.path.join(tmpdir, 'instance_batch.py')) as f:
                source = f.read()

            assert 'BatchResource' in source
            assert 'name_prefix' in source
            assert 'count' in source
            assert 'topology_optimized' in source
