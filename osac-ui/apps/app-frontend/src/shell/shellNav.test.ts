import { describe, expect, it } from 'vitest';

import type { UserRole } from '@osac/ui-components/shellTypes';
import { tIdentity } from '@osac/ui-components/test-utils/i18n';

import { NavLink, NavSection, isNavLink, isNavSection, navRowsForRole } from './shellNav';

const nonIdpRoles: UserRole[] = ['tenant-admin', 'tenant-user'];

const findLink = (role: UserRole, linkId: string): NavLink | undefined =>
  navRowsForRole(role, tIdentity).find((row) => isNavLink(row) && row.id === linkId) as
    | NavLink
    | undefined;

const findSection = (role: UserRole, sectionId: string): NavSection | undefined =>
  navRowsForRole(role, tIdentity).find((row) => isNavSection(row) && row.id === sectionId) as
    | NavSection
    | undefined;

const servicesChildren = (role: UserRole) =>
  findSection(role, 'nav-tenant-services')?.children ?? [];

describe('navRowsForRole', () => {
  it('includes Virtual Machines, Clusters, and Bare Metal under Services for tenant roles', () => {
    for (const role of nonIdpRoles) {
      expect(servicesChildren(role)).toEqual([
        { kind: 'link', id: 'bare-metal', label: 'Bare Metal', path: '/bare-metal' },
        { kind: 'link', id: 'clusters', label: 'Clusters', path: '/clusters' },
        { kind: 'link', id: 'compute-vms', label: 'Virtual Machines', path: '/vms' },
      ]);
    }
    expect(servicesChildren('tenant-idp-manager')).toEqual([]);
  });

  it('excludes Services section for admin (cloud-provider-admin) role', () => {
    expect(findSection('admin', 'nav-tenant-services')).toBeUndefined();
  });

  it('includes Catalog and Projects for admin and tenant roles but not IDP manager', () => {
    for (const role of [...nonIdpRoles, 'admin'] as UserRole[]) {
      expect(findLink(role, 'catalog')).toBeDefined();
      expect(findLink(role, 'projects')).toBeDefined();
    }
    expect(findLink('tenant-idp-manager', 'catalog')).toBeUndefined();
    expect(findLink('tenant-idp-manager', 'projects')).toBeUndefined();
  });

  it('includes Networking section for all roles except IDP manager', () => {
    for (const role of [...nonIdpRoles, 'admin'] as UserRole[]) {
      const networking = findSection(role, 'nav-tenant-networking');
      expect(networking).toBeDefined();
      expect(networking?.children).toEqual([
        {
          kind: 'link',
          id: 'virtual-networks',
          label: 'Virtual networks',
          path: '/networking/virtual-networks',
        },
        {
          kind: 'link',
          id: 'security-groups',
          label: 'Security groups',
          path: '/networking/security-groups',
        },
        {
          kind: 'link',
          id: 'external-ips',
          label: 'External IPs',
          path: '/networking/external-ips',
        },
      ]);
    }
    const networking = findSection('tenant-idp-manager', 'nav-tenant-networking');
    expect(networking).toBeUndefined();
  });

  it('Administration section shows up only for admin role', () => {
    expect(findSection('admin', 'nav-administration')).toBeDefined();
    for (const role of ['tenant-user', 'tenant-admin', 'tenant-idp-manager'] as UserRole[]) {
      expect(findSection(role, 'nav-administration')).toBeUndefined();
    }
  });

  it('includes only Tenants under Administration for admin role', () => {
    expect(findSection('admin', 'nav-administration')?.children).toEqual([
      { kind: 'link', id: 'tenant', label: 'Tenants', path: '/admin/tenants' },
    ]);
  });

  it('Infrastructure section shows up only for admin role and contains storage, instance types, external IP pools, bare metal instance types, and disk images', () => {
    expect(findSection('admin', 'nav-infrastructure')).toEqual({
      kind: 'section',
      id: 'nav-infrastructure',
      label: 'Infrastructure',
      children: [
        {
          kind: 'link',
          id: 'storage',
          label: 'Storage',
          path: '/admin/infrastructure/storage',
        },
        {
          kind: 'link',
          id: 'instance-types',
          label: 'Instance types',
          path: '/admin/infrastructure/instance-types',
        },
        {
          kind: 'link',
          id: 'external-ip-pools',
          label: 'External IP pools',
          path: '/admin/infrastructure/external-ip-pools',
        },
        {
          kind: 'link',
          id: 'baremetal-instance-types',
          label: 'Bare metal instance types',
          path: '/admin/infrastructure/baremetal-instance-types',
        },
        {
          kind: 'link',
          id: 'disk-images',
          label: 'Disk images',
          path: '/admin/infrastructure/disk-images',
        },
      ],
    });
    for (const role of ['tenant-user', 'tenant-admin', 'tenant-idp-manager'] as UserRole[]) {
      expect(findSection(role, 'nav-infrastructure')).toBeUndefined();
    }
  });

  it('IDP sections show for tenant-idp-manager and tenant-admin', () => {
    for (const role of ['tenant-idp-manager', 'tenant-admin'] as UserRole[]) {
      expect(findLink(role, 'idp')).toBeDefined();
      expect(findLink(role, 'role-bindings')).toBeDefined();
    }

    const catalogLink = findLink('tenant-idp-manager', 'catalog');
    expect(catalogLink).toBeUndefined();
  });
});
