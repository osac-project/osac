import { deleteResourceModalIds } from '../ui-components/src/components/Resource/deleteResourceModalIds';
import { storageTierFieldIds } from '../ui-components/src/components/Storage/fieldIds';

type StorageLocator =
  | readonly [
      role: 'link' | 'heading' | 'button' | 'grid' | 'tab' | 'radio' | 'menuitem',
      options: { name: string; exact: true },
    ]
  | readonly [label: string]
  | readonly [selector: string];

const id = (value: string): readonly [string] => [`#${value}`];

export const StorageBackendsPage = {
  pageTitle: ['heading', { name: 'Storage Backends', exact: true }],
  backendsTable: ['grid', { name: 'Storage backends', exact: true }],
} as const satisfies Record<string, StorageLocator>;

export const StorageTiersPage = {
  pageTitle: ['heading', { name: 'Storage Tiers', exact: true }],
  tiersTable: ['grid', { name: 'Storage tiers', exact: true }],
  createTier: ['link', { name: 'Create tier', exact: true }],
} as const satisfies Record<string, StorageLocator>;

export const StorageTierCreatePage = {
  pageTitle: ['heading', { name: 'Create storage tier', exact: true }],
  name: id(storageTierFieldIds.name),
  description: id(storageTierFieldIds.description),
  backend: id(storageTierFieldIds.backend),
  protocolGroup: [
    `[role="radiogroup"][aria-labelledby="${storageTierFieldIds.protocol}-legend"]`,
  ],
  nfsProtocol: ['radio', { name: 'NFS', exact: true }],
  blockProtocol: ['radio', { name: 'Block', exact: true }],
  create: ['button', { name: 'Create', exact: true }],
} as const satisfies Record<string, StorageLocator>;

export const StorageTierDetailsPage = {
  tiersListBreadcrumb: ['link', { name: 'Storage tiers', exact: true }],
} as const satisfies Record<string, StorageLocator>;

export const StorageTierDeletePage = {
  deleteMenuItem: ['menuitem', { name: 'Delete', exact: true }],
  deleteDialog: [`[role="dialog"][aria-labelledby="${deleteResourceModalIds.title}"]`],
  deleteTitle: [`#${deleteResourceModalIds.title}`],
  deleteWarning: ['This permanently deletes the storage tier. This action cannot be undone.'],
  confirmDelete: ['button', { name: 'Delete', exact: true }],
} as const satisfies Record<string, StorageLocator>;
