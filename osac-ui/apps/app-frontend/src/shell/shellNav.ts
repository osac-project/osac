/** Role-based sidebar navigation (sectioned NavGroup layout). Nav icons: shellNavIcon in @osac/ui-components/icons */
import type { TFunction } from 'i18next';

import { ServiceTier } from '@osac/types';
import type { UserRole } from '@osac/ui-components/shellTypes';

export const isNavSection = (row: NavRow): row is NavSection => row.kind === 'section';
export const isNavLink = (row: NavRow): row is NavLink => row.kind === 'link';

export type NavLink = {
  kind: 'link';
  id: string;
  label: string;
  path: string;
  service?: ServiceTier;
};

export type NavSection = {
  kind: 'section';
  id: string;
  label: string;
  children: NavLink[];
};

export type NavRow = NavSection | NavLink;

export const filterNavRowsByServices = (
  rows: NavRow[],
  enabledServices: readonly ServiceTier[],
): NavRow[] =>
  rows.flatMap((row): NavRow[] => {
    if (!isNavSection(row)) {
      return row.service && !enabledServices.includes(row.service) ? [] : [row];
    }

    const children = row.children.filter(
      (child) => !child.service || enabledServices.includes(child.service),
    );
    return children.length > 0 ? [{ ...row, children }] : [];
  });

const getIdpLinks = (t: TFunction): NavRow[] => [
  { kind: 'link', id: 'idp', label: t('Identity providers'), path: '/tenant/identity-provider' },
  { kind: 'link', id: 'role-bindings', label: t('Role Bindings'), path: '/tenant/role-binding' },
];

const getIdpManagerNav = (t: TFunction): NavRow[] => [...getIdpLinks(t), getSecretsNav(t)];

const getAdminNav = (t: TFunction): NavRow[] => [
  getCatalogNav(t),
  getProjectsNav(t),
  {
    kind: 'section',
    id: 'nav-administration',
    label: t('Administration'),
    children: [{ kind: 'link', id: 'tenant', label: t('Tenants'), path: '/admin/tenants' }],
  },
  {
    kind: 'section',
    id: 'nav-infrastructure',
    label: t('Infrastructure'),
    children: [
      {
        kind: 'link',
        id: 'storage',
        label: t('Storage'),
        path: '/admin/infrastructure/storage',
      },
      {
        kind: 'link',
        id: 'instance-types',
        label: t('Instance types'),
        path: '/admin/infrastructure/instance-types',
      },
      {
        kind: 'link',
        id: 'external-ip-pools',
        label: t('External IP pools'),
        path: '/admin/infrastructure/external-ip-pools',
      },
      {
        kind: 'link',
        id: 'baremetal-instance-types',
        label: t('Bare metal instance types'),
        path: '/admin/infrastructure/baremetal-instance-types',
      },
      {
        kind: 'link',
        id: 'disk-images',
        label: t('Disk images'),
        path: '/admin/infrastructure/disk-images',
      },
    ],
  },
  getNetworkNav(t),
  getSecretsNav(t),
];

const getTenantAdminNav = (t: TFunction): NavRow[] => [
  ...getBaseNav(t),
  ...getIdpLinks(t),
  getNetworkNav(t),
  getSecretsNav(t),
];

const getCatalogNav = (t: TFunction): NavRow => ({
  kind: 'link',
  id: 'catalog',
  label: t('Catalog'),
  path: '/catalog',
});

const getSecretsNav = (t: TFunction): NavRow => ({
  kind: 'link',
  id: 'secrets',
  label: t('Secrets'),
  path: '/secrets',
});

const getServicesNav = (t: TFunction): NavRow => ({
  kind: 'section',
  id: 'nav-tenant-services',
  label: t('Services'),
  children: [
    {
      kind: 'link',
      id: 'bare-metal',
      label: t('Bare Metal'),
      path: '/bare-metal',
      service: ServiceTier.BMAAS,
    },
    {
      kind: 'link',
      id: 'clusters',
      label: t('Clusters'),
      path: '/clusters',
      service: ServiceTier.CAAS,
    },
    {
      kind: 'link',
      id: 'compute-vms',
      label: t('Virtual Machines'),
      path: '/vms',
      service: ServiceTier.VMAAS,
    },
  ],
});

const getProjectsNav = (t: TFunction): NavRow => ({
  kind: 'link',
  id: 'projects',
  label: t('Projects'),
  path: '/projects',
});

const getNetworkNav = (t: TFunction): NavRow => ({
  kind: 'section',
  id: 'nav-tenant-networking',
  label: t('Networking'),
  children: [
    {
      kind: 'link',
      id: 'virtual-networks',
      label: t('Virtual networks'),
      path: '/networking/virtual-networks',
    },
    {
      kind: 'link',
      id: 'security-groups',
      label: t('Security groups'),
      path: '/networking/security-groups',
    },
  ],
});

const getBaseNav = (t: TFunction): NavRow[] => [
  getCatalogNav(t),
  getServicesNav(t),
  getProjectsNav(t),
];

export const navRowsForRole = (
  role: UserRole,
  t: TFunction,
  enabledServices: readonly ServiceTier[],
): NavRow[] => {
  let rows: NavRow[];

  if (role === 'admin') {
    rows = getAdminNav(t);
  } else if (role === 'tenant-idp-manager') {
    rows = getIdpManagerNav(t);
  } else if (role === 'tenant-admin') {
    rows = getTenantAdminNav(t);
  } else {
    // 'tenant-user'
    rows = [...getBaseNav(t), getNetworkNav(t), getSecretsNav(t)];
  }

  return filterNavRowsByServices(rows, enabledServices);
};
