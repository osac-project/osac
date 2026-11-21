--
-- Copyright (c) 2026 Red Hat Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--

-- Prepare permissions for named roles.
-- This migration creates no bindings, and seeds roles with
-- bindable subjects (users or managed service accounts)
insert into roles (id, name, tenant, data) values
(
  '019dd9f6-36cf-77bc-8041-c6a6a415c185',
  'tenant-user',
  'shared',
  '{"spec":{"title":"Tenant user", "description":"Standard user access within a tenant."},"status":{"state":"ROLE_STATE_READY"}}'
);

with seeded_permissions (id, permissions) as (
  values
  -- cloud-provider-admin
  ('019dd9f6-36cb-7824-ac1c-6455d8a7a9b5', '[
      {"service": "*", "methods": ["*"]}
    ]'::jsonb),
  -- cloud-provider-reader
  ('019dd9f6-36cc-7a35-bfc5-242833f11c2f', '[
      {"service": "osac.public.*", "methods": ["get", "list"]},
      {"service": "osac.public.events", "methods": ["watch"]},
      {"service": "osac.public.selfsubjectaccessreviews", "methods": ["create"]}
    ]'::jsonb),
  -- tenant-admin
  ('019dd9f6-36cd-78bf-9fa2-1be4464108b0', '[
      {"service": "osac.public.addonoperators", "methods": ["get", "list"]},
      {"service": "osac.public.baremetalinstancecatalogitems", "methods": ["*"]},
      {"service": "osac.public.baremetalinstances", "methods": ["*"]},
      {"service": "osac.public.baremetalinstancetemplates", "methods": ["get", "list"]},
      {"service": "osac.public.baremetalinstancetypes", "methods": ["get", "list"]},
      {"service": "osac.public.clustercatalogitems", "methods": ["*"]},
      {"service": "osac.public.clusters", "methods": ["*"]},
      {"service": "osac.public.clustertemplates", "methods": ["get", "list"]},
      {"service": "osac.public.clusterversions", "methods": ["get", "list"]},
      {"service": "osac.public.computeinstancecatalogitems", "methods": ["*"]},
      {"service": "osac.public.computeinstances", "methods": ["*"]},
      {"service": "osac.public.computeinstancetemplates", "methods": ["get", "list"]},
      {"service": "osac.public.consolesessions", "methods": ["create"]},
      {"service": "osac.public.diskimages", "methods": ["*"]},
      {"service": "osac.public.events", "methods": ["watch"]},
      {"service": "osac.public.externalipattachments", "methods": ["*"]},
      {"service": "osac.public.externalippools", "methods": ["get", "list"]},
      {"service": "osac.public.externalips", "methods": ["*"]},
      {"service": "osac.public.fabricdomains", "methods": ["get", "list"]},
      {"service": "osac.public.identityproviders", "methods": ["*"]},
      {"service": "osac.public.instancetypes", "methods": ["get", "list"]},
      {"service": "osac.public.natgateways", "methods": ["*"]},
      {"service": "osac.public.projectmemberships", "methods": ["*"]},
      {"service": "osac.public.projects", "methods": ["*"]},
      {"service": "osac.public.rolebindings", "methods": ["*"]},
      {"service": "osac.public.roles", "methods": ["*"]},
      {"service": "osac.public.secrets", "methods": ["*"]},
      {"service": "osac.public.securitygroups", "methods": ["*"]},
      {"service": "osac.public.selfsubjectaccessreviews", "methods": ["create"]},
      {"service": "osac.public.serviceaccounts", "methods": ["*"]},
      {"service": "osac.public.storagetiers", "methods": ["get", "list"]},
      {"service": "osac.public.subnets", "methods": ["*"]},
      {"service": "osac.public.tenants", "methods": ["list"]},
      {"service": "osac.public.users", "methods": ["*"]},
      {"service": "osac.public.virtualnetworks", "methods": ["*"]},
      {"service": "osac.public.volumes", "methods": ["*"]}
    ]'::jsonb),
  -- tenant-reader
  ('019dd9f6-36ce-78fc-96d8-b87cf58bf14c', '[
      {"service": "osac.public.addonoperators", "methods": ["get", "list"]},
      {"service": "osac.public.baremetalinstancecatalogitems", "methods": ["get", "list"]},
      {"service": "osac.public.baremetalinstances", "methods": ["get", "list"]},
      {"service": "osac.public.baremetalinstancetemplates", "methods": ["get", "list"]},
      {"service": "osac.public.baremetalinstancetypes", "methods": ["get", "list"]},
      {"service": "osac.public.clustercatalogitems", "methods": ["get", "list"]},
      {"service": "osac.public.clusters", "methods": ["get", "list"]},
      {"service": "osac.public.clustertemplates", "methods": ["get", "list"]},
      {"service": "osac.public.clusterversions", "methods": ["get", "list"]},
      {"service": "osac.public.computeinstancecatalogitems", "methods": ["get", "list"]},
      {"service": "osac.public.computeinstances", "methods": ["get", "list"]},
      {"service": "osac.public.computeinstancetemplates", "methods": ["get", "list"]},
      {"service": "osac.public.diskimages", "methods": ["get", "list"]},
      {"service": "osac.public.events", "methods": ["watch"]},
      {"service": "osac.public.externalipattachments", "methods": ["get", "list"]},
      {"service": "osac.public.externalippools", "methods": ["get", "list"]},
      {"service": "osac.public.externalips", "methods": ["get", "list"]},
      {"service": "osac.public.fabricdomains", "methods": ["get", "list"]},
      {"service": "osac.public.instancetypes", "methods": ["get", "list"]},
      {"service": "osac.public.natgateways", "methods": ["get", "list"]},
      {"service": "osac.public.projectmemberships", "methods": ["list"]},
      {"service": "osac.public.projects", "methods": ["get", "list"]},
      {"service": "osac.public.rolebindings", "methods": ["get", "list"]},
      {"service": "osac.public.roles", "methods": ["get", "list"]},
      {"service": "osac.public.secrets", "methods": ["get", "list"]},
      {"service": "osac.public.securitygroups", "methods": ["get", "list"]},
      {"service": "osac.public.selfsubjectaccessreviews", "methods": ["create"]},
      {"service": "osac.public.storagetiers", "methods": ["get", "list"]},
      {"service": "osac.public.subnets", "methods": ["get", "list"]},
      {"service": "osac.public.tenants", "methods": ["list"]},
      {"service": "osac.public.virtualnetworks", "methods": ["get", "list"]},
      {"service": "osac.public.volumes", "methods": ["get", "list"]}
    ]'::jsonb),
  -- tenant-user
  ('019dd9f6-36cf-77bc-8041-c6a6a415c185', '[
      {"service": "osac.public.addonoperators", "methods": ["get", "list"]},
      {"service": "osac.public.baremetalinstancecatalogitems", "methods": ["get", "list"]},
      {"service": "osac.public.baremetalinstances", "methods": ["create", "delete", "get", "list", "update"]},
      {"service": "osac.public.baremetalinstancetemplates", "methods": ["get", "list"]},
      {"service": "osac.public.baremetalinstancetypes", "methods": ["get", "list"]},
      {"service": "osac.public.clustercatalogitems", "methods": ["get", "list"]},
      {"service": "osac.public.clusters", "methods": ["create", "delete", "get", "list", "update"]},
      {"service": "osac.public.clustertemplates", "methods": ["get", "list"]},
      {"service": "osac.public.clusterversions", "methods": ["get", "list"]},
      {"service": "osac.public.computeinstancecatalogitems", "methods": ["get", "list"]},
      {"service": "osac.public.computeinstances", "methods": ["create", "delete", "get", "list", "update"]},
      {"service": "osac.public.computeinstancetemplates", "methods": ["get", "list"]},
      {"service": "osac.public.consolesessions", "methods": ["create"]},
      {"service": "osac.public.diskimages", "methods": ["create", "delete", "get", "list", "update"]},
      {"service": "osac.public.events", "methods": ["watch"]},
      {"service": "osac.public.externalipattachments", "methods": ["create", "delete", "get", "list"]},
      {"service": "osac.public.externalippools", "methods": ["get", "list"]},
      {"service": "osac.public.externalips", "methods": ["create", "delete", "get", "list"]},
      {"service": "osac.public.fabricdomains", "methods": ["get", "list"]},
      {"service": "osac.public.instancetypes", "methods": ["get", "list"]},
      {"service": "osac.public.natgateways", "methods": ["create", "delete", "get", "list"]},
      {"service": "osac.public.projectmemberships", "methods": ["list"]},
      {"service": "osac.public.projects", "methods": ["get", "list"]},
      {"service": "osac.public.rolebindings", "methods": ["get", "list"]},
      {"service": "osac.public.roles", "methods": ["get", "list"]},
      {"service": "osac.public.secrets", "methods": ["create", "delete", "get", "list", "update"]},
      {"service": "osac.public.securitygroups", "methods": ["create", "delete", "get", "list", "update"]},
      {"service": "osac.public.selfsubjectaccessreviews", "methods": ["create"]},
      {"service": "osac.public.storagetiers", "methods": ["get", "list"]},
      {"service": "osac.public.subnets", "methods": ["create", "delete", "get", "list"]},
      {"service": "osac.public.tenants", "methods": ["list"]},
      {"service": "osac.public.virtualnetworks", "methods": ["create", "delete", "get", "list"]},
      {"service": "osac.public.volumes", "methods": ["create", "delete", "get", "list", "update"]}
    ]'::jsonb),
  -- tenant-idp-manager
  ('019dd9f6-d573-beaa-41c5-96965e594cc6', '[
      {"service": "osac.public.addonoperators", "methods": ["get", "list"]},
      {"service": "osac.public.baremetalinstancecatalogitems", "methods": ["get", "list"]},
      {"service": "osac.public.baremetalinstances", "methods": ["create", "delete", "get", "list", "update"]},
      {"service": "osac.public.baremetalinstancetemplates", "methods": ["get", "list"]},
      {"service": "osac.public.baremetalinstancetypes", "methods": ["get", "list"]},
      {"service": "osac.public.clustercatalogitems", "methods": ["get", "list"]},
      {"service": "osac.public.clusters", "methods": ["create", "delete", "get", "list", "update"]},
      {"service": "osac.public.clustertemplates", "methods": ["get", "list"]},
      {"service": "osac.public.clusterversions", "methods": ["get", "list"]},
      {"service": "osac.public.computeinstancecatalogitems", "methods": ["get", "list"]},
      {"service": "osac.public.computeinstances", "methods": ["create", "delete", "get", "list", "update"]},
      {"service": "osac.public.computeinstancetemplates", "methods": ["get", "list"]},
      {"service": "osac.public.consolesessions", "methods": ["create"]},
      {"service": "osac.public.diskimages", "methods": ["create", "delete", "get", "list", "update"]},
      {"service": "osac.public.events", "methods": ["watch"]},
      {"service": "osac.public.externalipattachments", "methods": ["create", "delete", "get", "list"]},
      {"service": "osac.public.externalippools", "methods": ["get", "list"]},
      {"service": "osac.public.externalips", "methods": ["create", "delete", "get", "list"]},
      {"service": "osac.public.fabricdomains", "methods": ["get", "list"]},
      {"service": "osac.public.identityproviders", "methods": ["create", "delete", "get", "list", "update"]},
      {"service": "osac.public.instancetypes", "methods": ["get", "list"]},
      {"service": "osac.public.natgateways", "methods": ["create", "delete", "get", "list"]},
      {"service": "osac.public.projectmemberships", "methods": ["list"]},
      {"service": "osac.public.projects", "methods": ["get", "list"]},
      {"service": "osac.public.rolebindings", "methods": ["create", "delete", "get", "list", "update"]},
      {"service": "osac.public.roles", "methods": ["get", "list"]},
      {"service": "osac.public.secrets", "methods": ["create", "delete", "get", "list", "update"]},
      {"service": "osac.public.securitygroups", "methods": ["create", "delete", "get", "list", "update"]},
      {"service": "osac.public.selfsubjectaccessreviews", "methods": ["create"]},
      {"service": "osac.public.storagetiers", "methods": ["get", "list"]},
      {"service": "osac.public.subnets", "methods": ["create", "delete", "get", "list"]},
      {"service": "osac.public.tenants", "methods": ["list"]},
      {"service": "osac.public.users", "methods": ["get", "list"]},
      {"service": "osac.public.virtualnetworks", "methods": ["create", "delete", "get", "list"]},
      {"service": "osac.public.volumes", "methods": ["create", "delete", "get", "list", "update"]}
    ]'::jsonb),
  -- tenant-automation
  ('cd33ed2b-5a5f-4b0a-adcb-5a0a567fa511', '[
      {"service": "osac.public.addonoperators", "methods": ["get", "list"]},
      {"service": "osac.public.baremetalinstancecatalogitems", "methods": ["get", "list"]},
      {"service": "osac.public.baremetalinstances", "methods": ["create", "delete", "get", "list", "update"]},
      {"service": "osac.public.baremetalinstancetemplates", "methods": ["get", "list"]},
      {"service": "osac.public.baremetalinstancetypes", "methods": ["get", "list"]},
      {"service": "osac.public.clustercatalogitems", "methods": ["get", "list"]},
      {"service": "osac.public.clusters", "methods": ["create", "delete", "get", "list", "update"]},
      {"service": "osac.public.clustertemplates", "methods": ["get", "list"]},
      {"service": "osac.public.clusterversions", "methods": ["get", "list"]},
      {"service": "osac.public.computeinstancecatalogitems", "methods": ["get", "list"]},
      {"service": "osac.public.computeinstances", "methods": ["create", "delete", "get", "list", "update"]},
      {"service": "osac.public.computeinstancetemplates", "methods": ["get", "list"]},
      {"service": "osac.public.diskimages", "methods": ["create", "delete", "get", "list", "update"]},
      {"service": "osac.public.externalipattachments", "methods": ["create", "delete", "get", "list"]},
      {"service": "osac.public.externalippools", "methods": ["get", "list"]},
      {"service": "osac.public.externalips", "methods": ["create", "delete", "get", "list"]},
      {"service": "osac.public.fabricdomains", "methods": ["get", "list"]},
      {"service": "osac.public.instancetypes", "methods": ["get", "list"]},
      {"service": "osac.public.natgateways", "methods": ["create", "delete", "get", "list"]},
      {"service": "osac.public.projectmemberships", "methods": ["list"]},
      {"service": "osac.public.projects", "methods": ["get", "list"]},
      {"service": "osac.public.rolebindings", "methods": ["get", "list"]},
      {"service": "osac.public.roles", "methods": ["get", "list"]},
      {"service": "osac.public.secrets", "methods": ["create", "delete", "list", "update"]},
      {"service": "osac.public.securitygroups", "methods": ["create", "delete", "get", "list", "update"]},
      {"service": "osac.public.selfsubjectaccessreviews", "methods": ["create"]},
      {"service": "osac.public.storagetiers", "methods": ["get", "list"]},
      {"service": "osac.public.subnets", "methods": ["create", "delete", "get", "list"]},
      {"service": "osac.public.tenants", "methods": ["list"]},
      {"service": "osac.public.virtualnetworks", "methods": ["create", "delete", "get", "list"]},
      {"service": "osac.public.volumes", "methods": ["create", "delete", "get", "list", "update"]}
    ]'::jsonb),
  -- osac-csi-driver
  ('6eafb57d-4344-43fc-86eb-8ce17749f5ec', '[
      {"service": "osac.private.volumes", "methods": ["create", "delete", "get", "list"]}
    ]'::jsonb)
)
update roles
set data = jsonb_set(roles.data, '{spec,permissions}', seeded_permissions.permissions)
from seeded_permissions
where roles.id = seeded_permissions.id;
