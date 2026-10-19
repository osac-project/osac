type TenantsLocator =
  | readonly [
      role: 'link' | 'button' | 'heading' | 'textbox' | 'grid' | 'group' | 'dialog' | 'menuitem',
      options: { name: string; exact: true },
    ]
  | readonly [label: string];

export const TenantsMainPage = {
  pageTitle: ['heading', { name: 'Tenants', exact: true }],
  createTenant: ['link', { name: 'Create tenant', exact: true }],
  searchTenants: ['textbox', { name: 'Search tenants', exact: true }],
  tenantsTable: ['grid', { name: 'Tenants', exact: true }],
  tenantRows: ['tbody tr'],
  tenantNameCell: ['td[data-label="Tenant"]'],
  tenantStatusCell: ['td[data-label="Status"]'],
  tenantPrimaryDomainCell: ['td[data-label="Primary domain"]'],
  tenantActionsToggle: ['button[aria-label^="Actions for "]'],
} as const satisfies Record<string, TenantsLocator>;

export const TenantsCreatePage = {
  createPageTitle: ['heading', { name: 'Create tenant', exact: true }],
  tenantsBreadcrumb: ['button', { name: 'Tenants', exact: true }],
  name: ['Name'],
  domain: ['Domain 1'],
  domains: ['group', { name: 'Domains', exact: true }],
  addDomain: ['button', { name: 'Add domain', exact: true }],
  create: ['button', { name: 'Create', exact: true }],
  cancel: ['button', { name: 'Cancel', exact: true }],
} as const satisfies Record<string, TenantsLocator>;

export const TenantsCredentialsPage = {
  breakGlassDialog: ['dialog', { name: 'Break-glass credentials', exact: true }],
  breakGlassWarning: ['Save these credentials now — they cannot be retrieved later.'],
  copyableInput: ['textbox', { name: 'Copyable input', exact: true }],
  savedCredentials: ['button', { name: 'I have saved the credentials', exact: true }],
} as const satisfies Record<string, TenantsLocator>;

export const TenantsDeletePage = {
  deleteMenuItem: ['menuitem', { name: 'Delete', exact: true }],
  deleteDialog: ['[role="dialog"][aria-labelledby="delete-confirm-title"]'],
  deleteTitle: ['#delete-confirm-title'],
  deleteWarning: [
    'This permanently deletes the tenant and all its resources. This action cannot be undone.',
  ],
  deleteButton: ['button', { name: 'Delete', exact: true }],
} as const satisfies Record<string, TenantsLocator>;
