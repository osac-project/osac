import { deleteResourceModalIds } from '../ui-components/src/components/Resource/deleteResourceModalIds';

const id = (value: string): readonly [string] => [`[id="${value}"]`];

type VmsLocator =
  | readonly [
      role:
        | 'button'
        | 'checkbox'
        | 'grid'
        | 'heading'
        | 'link'
        | 'radio'
        | 'switch'
        | 'term'
        | 'textbox',
      options: { name: string; exact: true },
    ]
  | readonly [role: 'term']
  | readonly [label: string]
  | readonly [selector: string];

export const VmsMainPage = {
  pageTitle: ['heading', { name: 'Virtual machines', exact: true }],
  vmsTable: ['grid', { name: 'Virtual machines', exact: true }],
  vmRows: ['tbody tr'],
  vmNameCell: ['td[data-label="Name"]'],
  vmStatusCell: ['td[data-label="Status"]'],
  listView: id('list-view'),
  emptyState: ['No virtual machines found'],
  filteredEmptyState: ['No virtual machines are available yet.'],
  noMatchingState: ['No virtual machines match your filters'],
  errorState: ['heading', { name: 'An error occurred', exact: true }],
} as const satisfies Record<string, VmsLocator>;

export const VmDetailsPage = {
  pageTitle: ['h1.pf-v6-c-title'],
  statusTerm: ['term'],
  statusLabel: ['.pf-v6-c-label'],
  instanceTypeEditButton: ['button', { name: 'Edit instance type', exact: true }],
  conditionsTable: ['grid', { name: 'Virtual machine conditions', exact: true }],
  deleteButton: ['button', { name: 'Delete', exact: true }],
  deleteDialog: [`[role="dialog"][aria-labelledby="${deleteResourceModalIds.title}"]`],
  deleteTitle: [`#${deleteResourceModalIds.title}`],
  deleteWarning: ['This permanently deletes the compute instance. This action cannot be undone.'],
} as const satisfies Record<string, VmsLocator>;

export const VmDetailsActionButtons = {
  openConsole: ['button', { name: 'Open console', exact: true }],
  start: ['button', { name: 'Start', exact: true }],
  stop: ['button', { name: 'Stop', exact: true }],
  restart: ['button', { name: 'Restart', exact: true }],
} as const satisfies Record<string, VmsLocator>;

export const VmCreatePage = {
  catalogItemCards: ['[id^="catalog-item-card-"]'],
  catalogItemSelection: ['input[id^="selectedCatalogItem-"]'],
  project: id('metadata.project'),
  vmName: id('metadata-name'),
  diskImage: id('disk-image'),
  instanceType: id('vm-instance-type'),
  bootDisk: id('vm-boot-disk-size'),
  bootStorageTier: id('vm-boot-disk-storage-tier'),
  storageTier: ['[id^="vm-additional-disk-"][id$="-storage-tier"]'],
  addDisk: ['button', { name: 'Add disk', exact: true }],
  additionalDiskSize: ['input[id^="vm-additional-disk-"][id$="-size"]'],
  useTenantDefaultNetwork: id('spec.networking.networkAttachment.value-choice-default'),
  autoExternalIp: id('spec.networking.autoExternalIpAttachment.value'),
  sshPublicKey: id('spec-sshKey-value'),
  cloudInit: id('inline-user-data'),
  next: ['button', { name: 'Next', exact: true }],
  create: ['button', { name: 'Create', exact: true }],
  configurationHeading: ['heading', { name: 'Configuration', exact: true }],
  storageHeading: ['heading', { name: 'Storage', exact: true }],
  userDataProvided: ['Provided (hidden)'],
} as const satisfies Record<string, VmsLocator>;
