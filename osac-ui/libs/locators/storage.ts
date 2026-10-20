type StorageLocator =
  | readonly [
      role: 'link' | 'heading' | 'button' | 'grid' | 'tab' | 'radio' | 'menuitem',
      options: { name: string; exact: true },
    ]
  | readonly [label: string]
  | readonly [selector: string];

export const StorageMainPage = {
  storageNavigationLink: ['link', { name: 'Storage', exact: true }],
  pageTitle: ['heading', { name: 'Storage', exact: true }],
  backendsTab: ['tab', { name: 'Backends', exact: true }],
  tiersTab: ['tab', { name: 'Tiers', exact: true }],
  backendsTable: ['grid', { name: 'Storage backends', exact: true }],
  tiersTable: ['grid', { name: 'Storage tiers', exact: true }],
  createTier: ['link', { name: 'Create tier', exact: true }],
} as const satisfies Record<string, StorageLocator>;

export const StorageTierCreatePage = {
  pageTitle: ['heading', { name: 'Create storage tier', exact: true }],
  name: ['Name'],
  description: ['Description'],
  backend: ['Backend'],
  nfsProtocol: ['radio', { name: 'NFS', exact: true }],
  blockProtocol: ['radio', { name: 'Block', exact: true }],
  create: ['button', { name: 'Create', exact: true }],
} as const satisfies Record<string, StorageLocator>;

export const StorageTierDetailsPage = {
  tiersListBreadcrumb: ['link', { name: 'Storage tiers', exact: true }],
} as const satisfies Record<string, StorageLocator>;

export const StorageTierDeletePage = {
  deleteMenuItem: ['menuitem', { name: 'Delete', exact: true }],
  deleteDialog: ['[role="dialog"][aria-labelledby="delete-confirm-title"]'],
  deleteWarning: ['This permanently deletes the storage tier. This action cannot be undone.'],
  confirmDelete: ['button', { name: 'Delete', exact: true }],
} as const satisfies Record<string, StorageLocator>;
