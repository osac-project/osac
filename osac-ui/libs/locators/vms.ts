import { deleteResourceModalIds } from '../ui-components/src/components/Resource/deleteResourceModalIds';

type VmsLocator =
  | readonly [role: 'button' | 'grid' | 'heading' | 'link', options: { name: string; exact: true }]
  | readonly [selector: string];

export const VmsMainPage = {
  pageTitle: ['heading', { name: 'Virtual machines', exact: true }],
  vmsTable: ['grid', { name: 'Virtual machines', exact: true }],
  vmRows: ['tbody tr'],
  vmNameCell: ['td[data-label="Name"]'],
  vmStatusCell: ['td[data-label="Status"]'],
  emptyState: ['No virtual machines yet. Create one to get started.'],
} as const satisfies Record<string, VmsLocator>;

export const VmDetailsPage = {
  pageTitle: ['h1.pf-v6-c-title'],
  statusLabel: ['.pf-v6-c-label'],
  deleteButton: ['button', { name: 'Delete', exact: true }],
  deleteDialog: [`[role="dialog"][aria-labelledby="${deleteResourceModalIds.title}"]`],
  deleteTitle: [`#${deleteResourceModalIds.title}`],
  deleteWarning: ['This permanently deletes the compute instance. This action cannot be undone.'],
} as const satisfies Record<string, VmsLocator>;
