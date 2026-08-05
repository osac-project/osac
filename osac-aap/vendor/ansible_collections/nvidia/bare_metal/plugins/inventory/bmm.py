# SPDX-FileCopyrightText: Copyright (c) 2026 Fabien Dupont
# SPDX-License-Identifier: Apache-2.0

from __future__ import absolute_import, division, print_function
__metaclass__ = type

DOCUMENTATION = r'''
---
name: nvidia.bare_metal.bmm
short_description: Dynamic inventory from NVIDIA Bare Metal Manager
description:
    - Queries the NVIDIA Bare Metal Manager API for instances and builds
      an Ansible inventory from the results.
    - Instances become hosts. IP addresses are extracted from instance
      interfaces. Labels, site, VPC, and instance type are mapped to
      host variables and groups.
    - Pre-populates groups from sites, VPCs, and instance types so the
      infrastructure topology is visible even when no instances exist.
    - Reuses the same HTTP client and auth as the nvidia.bare_metal modules.
version_added: "1.0.0"
author: NVIDIA Bare Metal Manager Dev Team

options:
    plugin:
        description: Must be C(nvidia.bare_metal.bmm).
        required: true
        choices: ['nvidia.bare_metal.bmm']
    api_url:
        description: URL of the NVIDIA Bare Metal Manager API.
        type: str
        required: true
        env:
            - name: NVIDIA_BMM_API_URL
    api_token:
        description: JWT bearer token for API authentication.
        type: str
        required: true
        env:
            - name: NVIDIA_BMM_API_TOKEN
    org:
        description: Organization name for API requests.
        type: str
        required: true
        env:
            - name: NVIDIA_BMM_ORG
    api_path_prefix:
        description:
            - API path prefix. Use C(carbide) for direct access, C(forge) for the NVIDIA proxy.
        type: str
        default: carbide
        env:
            - name: NVIDIA_BMM_API_PATH_PREFIX
    filters:
        description:
            - Filter instances by API query parameters.
            - Keys are snake_case parameter names (e.g., C(site_id), C(vpc_id), C(status)).
        type: dict
        default: {}
    populate_topology:
        description:
            - Query sites, VPCs, and instance types to pre-create groups
              even when no instances exist yet.
        type: bool
        default: true
    ansible_host_source:
        description:
            - Where to get the C(ansible_host) value for each instance.
            - C(first_interface_ip) uses the first IP address from the first interface.
            - C(name) uses the instance name (useful with DNS).
        type: str
        default: first_interface_ip
        choices: ['first_interface_ip', 'name']
    group_by_labels:
        description:
            - Create groups from instance label key-value pairs.
            - Group names are formed as C(label_KEY_VALUE).
        type: bool
        default: true
    group_prefix:
        description:
            - Prefix for auto-created group names.
        type: str
        default: bmm_
    compose:
        description:
            - Jinja2 expressions to create host variables.
            - See Ansible constructed inventory documentation.
        type: dict
        default: {}
    groups:
        description:
            - Jinja2 conditionals to assign hosts to groups.
            - See Ansible constructed inventory documentation.
        type: dict
        default: {}
    keyed_groups:
        description:
            - Create groups based on variable values with a key prefix.
            - See Ansible constructed inventory documentation.
        type: list
        elements: dict
        default: []
    strict:
        description:
            - If true, raise errors on Jinja2 template failures in compose/groups.
        type: bool
        default: false

extends_documentation_fragment:
    - constructed
'''

EXAMPLES = r'''
---
# File: inventory/bmm.bmm.yml
# Discovers all Ready instances at a specific site.

plugin: nvidia.bare_metal.bmm
api_url: https://bmm-api.example.com
api_path_prefix: forge
org: my-org

filters:
  status: Ready
  site_id: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx

# Show sites, VPCs, instance types as groups even if empty
populate_topology: true

# Also create groups from labels (e.g., bmm_label_cluster_nvl72)
group_by_labels: true

# Set ansible_user for all discovered hosts
compose:
  ansible_user: "'root'"

# Create a "gpu_nodes" group from a label
groups:
  gpu_nodes: labels.get('role') == 'compute'
'''

import os

from ansible.plugins.inventory import BaseInventoryPlugin, Constructable
from ansible.errors import AnsibleParserError

from ansible_collections.nvidia.bare_metal.plugins.module_utils.common import (
    camel_to_snake,
    snake_to_camel,
    convert_keys,
)


class _InventoryModule(object):
    """Thin adapter that looks like an AnsibleModule to BareMetalClient."""

    def __init__(self, api_url, api_token, org, api_path_prefix):
        self.params = {
            'api_url': api_url,
            'api_token': api_token,
            'org': org,
            'api_path_prefix': api_path_prefix,
        }

    def fail_json(self, msg, **kwargs):
        raise AnsibleParserError(msg)


class InventoryModule(BaseInventoryPlugin, Constructable):

    NAME = 'nvidia.bare_metal.bmm'

    def verify_file(self, path):
        """Accept any YAML file that could be a BMM inventory source."""
        if super(InventoryModule, self).verify_file(path):
            if path.endswith(('.yml', '.yaml')):
                return True
        return False

    def parse(self, inventory, loader, path, cache=True):
        super(InventoryModule, self).parse(inventory, loader, path, cache)
        self._read_config_data(path)

        api_url = self.get_option('api_url')
        api_token = self.get_option('api_token')
        org = self.get_option('org')
        api_path_prefix = self.get_option('api_path_prefix')
        filters = self.get_option('filters')
        host_source = self.get_option('ansible_host_source')
        populate_topology = self.get_option('populate_topology')
        group_by_labels = self.get_option('group_by_labels')
        group_prefix = self.get_option('group_prefix')
        strict = self.get_option('strict')

        if not api_url or not api_token or not org:
            raise AnsibleParserError(
                'api_url, api_token, and org are required '
                '(set in the inventory file or via NVIDIA_BMM_* env vars)'
            )

        # Import the client here so the module_utils path is resolved at runtime
        from ansible_collections.nvidia.bare_metal.plugins.module_utils.client import BareMetalClient

        fake_module = _InventoryModule(api_url, api_token, org, api_path_prefix)
        client = BareMetalClient(fake_module)

        # Create the catch-all group
        all_group = '%sall' % group_prefix
        self.inventory.add_group(all_group)

        # ----------------------------------------------------------------
        # Topology groups — pre-populate from API resources
        # ----------------------------------------------------------------
        if populate_topology:
            self._populate_topology(client, group_prefix)

        # ----------------------------------------------------------------
        # Instance hosts
        # ----------------------------------------------------------------
        query_params = {}
        for k, v in (filters or {}).items():
            query_params[snake_to_camel(k)] = v

        instance_path = '/v2/org/{org}/carbide/instance'
        instances = client.list_all(instance_path, params=query_params)

        for raw_instance in instances:
            instance = convert_keys(raw_instance, camel_to_snake)
            name = instance.get('name')
            if not name:
                name = instance.get('id', 'unknown')

            self.inventory.add_host(name, group=all_group)

            # Set ansible_host
            if host_source == 'first_interface_ip':
                ansible_host = self._extract_first_ip(instance)
                if ansible_host:
                    self.inventory.set_variable(name, 'ansible_host', ansible_host)
            elif host_source == 'name':
                self.inventory.set_variable(name, 'ansible_host', name)

            # Set all instance fields as host vars under a 'bmm' namespace
            self.inventory.set_variable(name, 'bmm', instance)

            # Also set commonly-needed fields as top-level vars
            for field in ('id', 'site_id', 'vpc_id', 'instance_type_id',
                          'machine_id', 'operating_system_id', 'status'):
                val = instance.get(field)
                if val is not None:
                    self.inventory.set_variable(name, 'bmm_%s' % field, val)

            # Set labels as top-level vars
            labels = instance.get('labels') or {}
            self.inventory.set_variable(name, 'bmm_labels', labels)

            # Add host to topology groups (site, vpc, instance_type, status)
            self._add_to_topology_groups(name, instance, group_prefix)

            # Auto-create groups from labels
            if group_by_labels and labels:
                for lk, lv in labels.items():
                    group_name = self._sanitize_group('%slabel_%s_%s' % (
                        group_prefix, lk, lv,
                    ))
                    self.inventory.add_group(group_name)
                    self.inventory.add_host(name, group=group_name)

            # Constructed features: compose, groups, keyed_groups
            self._set_composite_vars(
                self.get_option('compose'), instance, name, strict=strict,
            )
            self._add_host_to_composed_groups(
                self.get_option('groups'), instance, name, strict=strict,
            )
            self._add_host_to_keyed_groups(
                self.get_option('keyed_groups'), instance, name, strict=strict,
            )

    def _populate_topology(self, client, group_prefix):
        """Query sites, VPCs, instance types, allocations, and IB partitions."""

        # --- Tenant (needed as a filter for downstream queries) ---
        tenant_path = '/v2/org/{org}/carbide/tenant/current'
        try:
            tenant = client.get(tenant_path)
        except Exception:
            tenant = None
        tenant_id = tenant.get('id', '') if tenant else ''

        if tenant:
            t = convert_keys(tenant, camel_to_snake)
            tenant_name = t.get('org_display_name') or t.get('org') or tenant_id
            group_name = self._sanitize_group('%stenant_%s' % (group_prefix, tenant_name))
            self.inventory.add_group(group_name)
            self.inventory.set_variable(group_name, 'bmm_tenant_id', tenant_id)
            self.inventory.set_variable(group_name, 'bmm_tenant_name', tenant_name)
            self._tenant_group = group_name
        else:
            self._tenant_group = None

        # --- Sites ---
        site_path = '/v2/org/{org}/carbide/site'
        try:
            sites = client.list_all(site_path, params={'tenantId': tenant_id} if tenant_id else None)
        except Exception:
            sites = []

        site_ids = []
        for raw_site in sites:
            site = convert_keys(raw_site, camel_to_snake)
            site_id = site.get('id', '')
            site_name = site.get('name', site_id)
            site_ids.append(site_id)
            group_name = self._sanitize_group('%ssite_%s' % (group_prefix, site_name))
            self.inventory.add_group(group_name)
            self.inventory.set_variable(group_name, 'bmm_site_id', site_id)
            self.inventory.set_variable(group_name, 'bmm_site_name', site_name)

        # --- VPCs ---
        vpc_path = '/v2/org/{org}/carbide/vpc'
        try:
            vpcs = client.list_all(vpc_path)
        except Exception:
            vpcs = []

        for raw_vpc in vpcs:
            vpc = convert_keys(raw_vpc, camel_to_snake)
            vpc_id = vpc.get('id', '')
            vpc_name = vpc.get('name', vpc_id)
            group_name = self._sanitize_group('%svpc_%s' % (group_prefix, vpc_name))
            self.inventory.add_group(group_name)
            self.inventory.set_variable(group_name, 'bmm_vpc_id', vpc_id)
            self.inventory.set_variable(group_name, 'bmm_vpc_name', vpc_name)

        # --- Instance types ---
        it_path = '/v2/org/{org}/carbide/instance/type'
        try:
            instance_types = client.list_all(it_path)
        except Exception:
            instance_types = []

        for raw_it in instance_types:
            it = convert_keys(raw_it, camel_to_snake)
            it_id = it.get('id', '')
            it_name = it.get('name', it_id)
            group_name = self._sanitize_group('%sinstance_type_%s' % (group_prefix, it_name))
            self.inventory.add_group(group_name)
            self.inventory.set_variable(group_name, 'bmm_instance_type_id', it_id)
            self.inventory.set_variable(group_name, 'bmm_instance_type_name', it_name)

        # --- Allocations (require tenantId filter) ---
        alloc_path = '/v2/org/{org}/carbide/allocation'
        allocations = []
        if tenant_id:
            for sid in site_ids:
                try:
                    page = client.list_all(alloc_path, params={
                        'tenantId': tenant_id,
                        'siteId': sid,
                    })
                    allocations.extend(page)
                except Exception:
                    pass

        for raw_alloc in allocations:
            alloc = convert_keys(raw_alloc, camel_to_snake)
            alloc_id = alloc.get('id', '')
            alloc_name = alloc.get('name', alloc_id)
            group_name = self._sanitize_group('%sallocation_%s' % (group_prefix, alloc_name))
            self.inventory.add_group(group_name)
            self.inventory.set_variable(group_name, 'bmm_allocation_id', alloc_id)
            self.inventory.set_variable(group_name, 'bmm_allocation_name', alloc_name)

        # --- InfiniBand partitions (require siteId filter) ---
        ib_path = '/v2/org/{org}/carbide/infiniband-partition'
        ib_partitions = []
        for sid in site_ids:
            try:
                page = client.list_all(ib_path, params={'siteId': sid})
                ib_partitions.extend(page)
            except Exception:
                pass

        for raw_ib in ib_partitions:
            ib = convert_keys(raw_ib, camel_to_snake)
            ib_id = ib.get('id', '')
            ib_name = ib.get('name', ib_id)
            group_name = self._sanitize_group('%sib_partition_%s' % (group_prefix, ib_name))
            self.inventory.add_group(group_name)
            self.inventory.set_variable(group_name, 'bmm_ib_partition_id', ib_id)
            self.inventory.set_variable(group_name, 'bmm_ib_partition_name', ib_name)

        # --- Build id->name lookup maps for host assignment ---
        self._site_map = {}
        for raw_site in sites:
            site = convert_keys(raw_site, camel_to_snake)
            self._site_map[site.get('id', '')] = site.get('name', site.get('id', ''))

        self._vpc_map = {}
        for raw_vpc in vpcs:
            vpc = convert_keys(raw_vpc, camel_to_snake)
            self._vpc_map[vpc.get('id', '')] = vpc.get('name', vpc.get('id', ''))

        self._it_map = {}
        for raw_it in instance_types:
            it = convert_keys(raw_it, camel_to_snake)
            self._it_map[it.get('id', '')] = it.get('name', it.get('id', ''))

        self._ib_map = {}
        for raw_ib in ib_partitions:
            ib = convert_keys(raw_ib, camel_to_snake)
            self._ib_map[ib.get('id', '')] = ib.get('name', ib.get('id', ''))

    def _add_to_topology_groups(self, hostname, instance, group_prefix):
        """Add a host to the appropriate topology groups."""
        # Tenant group
        tenant_group = getattr(self, '_tenant_group', None)
        if tenant_group:
            self.inventory.add_host(hostname, group=tenant_group)

        # Site group
        site_id = instance.get('site_id', '')
        if site_id:
            site_name = getattr(self, '_site_map', {}).get(site_id, site_id)
            group_name = self._sanitize_group('%ssite_%s' % (group_prefix, site_name))
            self.inventory.add_group(group_name)
            self.inventory.add_host(hostname, group=group_name)

        # VPC group
        vpc_id = instance.get('vpc_id', '')
        if vpc_id:
            vpc_name = getattr(self, '_vpc_map', {}).get(vpc_id, vpc_id)
            group_name = self._sanitize_group('%svpc_%s' % (group_prefix, vpc_name))
            self.inventory.add_group(group_name)
            self.inventory.add_host(hostname, group=group_name)

        # Instance type group
        it_id = instance.get('instance_type_id', '')
        if it_id:
            it_name = getattr(self, '_it_map', {}).get(it_id, it_id)
            group_name = self._sanitize_group('%sinstance_type_%s' % (group_prefix, it_name))
            self.inventory.add_group(group_name)
            self.inventory.add_host(hostname, group=group_name)

        # Status group
        status = instance.get('status', '')
        if status:
            group_name = self._sanitize_group('%sstatus_%s' % (group_prefix, status))
            self.inventory.add_group(group_name)
            self.inventory.add_host(hostname, group=group_name)

        # InfiniBand partition groups (from instance interfaces)
        ib_map = getattr(self, '_ib_map', {})
        seen_partitions = set()
        for iface in instance.get('infiniband_interfaces') or []:
            partition_id = iface.get('partition_id', '')
            if partition_id and partition_id not in seen_partitions:
                seen_partitions.add(partition_id)
                ib_name = ib_map.get(partition_id, partition_id)
                group_name = self._sanitize_group('%sib_partition_%s' % (group_prefix, ib_name))
                self.inventory.add_group(group_name)
                self.inventory.add_host(hostname, group=group_name)

    def _extract_first_ip(self, instance):
        """Extract the first IP address from the instance's interfaces."""
        for iface_field in ('interfaces', 'machine_interfaces'):
            interfaces = instance.get(iface_field) or []
            for iface in interfaces:
                ips = iface.get('ip_addresses') or []
                if ips:
                    return ips[0]
        return None

    def _sanitize_group(self, name):
        """Make a string safe for use as an Ansible group name."""
        return ''.join(c if c.isalnum() or c == '_' else '_' for c in name)
