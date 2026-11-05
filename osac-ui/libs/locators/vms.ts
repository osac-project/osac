import { deleteResourceModalIds } from '../ui-components/src/components/Resource/deleteResourceModalIds';

type VmsLocator =
  | readonly [
      role: 'button' | 'checkbox' | 'grid' | 'heading' | 'link' | 'textbox',
      options: { name: string; exact: true },
    ]
  | readonly [label: string]
  | readonly [selector: string];

export const VmsMainPage = {
  pageTitle: ['heading', { name: 'Virtual machines', exact: true }],
  vmsTable: ['grid', { name: 'Virtual machines', exact: true }],
  vmRows: ['tbody tr'],
  vmNameCell: ['td[data-label="Name"]'],
  vmStatusCell: ['td[data-label="Status"]'],
  emptyState: ['No virtual machines yet. Create one to get started.'],
  filteredEmptyState: ['Choose one or more statuses above to filter virtual machines.'],
  noMatchingState: ['No virtual machines match your filters.'],
  errorState: ['heading', { name: 'An error occurred', exact: true }],
} as const satisfies Record<string, VmsLocator>;

export const VmDetailsPage = {
  pageTitle: ['h1.pf-v6-c-title'],
  statusLabel: ['.pf-v6-c-label'],
  deleteButton: ['button', { name: 'Delete', exact: true }],
  deleteDialog: [`[role="dialog"][aria-labelledby="${deleteResourceModalIds.title}"]`],
  deleteTitle: [`#${deleteResourceModalIds.title}`],
  deleteWarning: ['This permanently deletes the compute instance. This action cannot be undone.'],
} as const satisfies Record<string, VmsLocator>;

export const VmCreatePage = {
  catalogItemCards: ['[id^="catalog-item-card-"]'],
  catalogItemSelection: ['input[type="radio"]'],
  project: ['button', { name: 'Project', exact: true }],
  vmName: ['textbox', { name: 'Name', exact: true }],
  diskImage: ['button', { name: 'Select the image reference', exact: true }],
  instanceType: ['button', { name: 'Instance type', exact: true }],
  bootDisk: ['Boot disk'],
  bootStorageTier: ['button', { name: 'Boot storage tier', exact: true }],
  storageTier: ['button', { name: 'Storage tier', exact: true }],
  addDisk: ['button', { name: 'Add disk', exact: true }],
  additionalDiskSize: ['Size (GiB)'],
  useTenantDefaultNetwork: ['checkbox', { name: 'Use tenant default network', exact: true }],
  autoExternalIp: ['checkbox', { name: 'Auto External IP Attachment', exact: true }],
  sshPublicKey: ['button', { name: 'SSH public key', exact: true }],
  cloudInit: ['#inline-user-data'],
  next: ['button', { name: 'Next', exact: true }],
  create: ['button', { name: 'Create', exact: true }],
  configurationHeading: ['heading', { name: 'Configuration', exact: true }],
  storageHeading: ['heading', { name: 'Storage', exact: true }],
} as const satisfies Record<string, VmsLocator>;
