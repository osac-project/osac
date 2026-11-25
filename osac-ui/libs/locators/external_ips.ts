import { deleteResourceModalIds } from '../ui-components/src/components/Resource/deleteResourceModalIds';

type ExternalIpLocator =
  | readonly [
      role:
        | 'button'
        | 'cell'
        | 'columnheader'
        | 'grid'
        | 'heading'
        | 'link'
        | 'menuitem'
        | 'option'
        | 'row'
        | 'textbox',
      options: { name: string; exact: true },
    ]
  | readonly [label: string]
  | readonly [selector: string];

const id = (value: string): readonly [string] => [`#${value}`];

export const ExternalIpsPage = {
  pageTitle: ['heading', { name: 'External IPs', exact: true }],
  externalIpsTable: ['grid', { name: 'External IPs', exact: true }],
  createExternalIp: ['link', { name: 'Create external IP', exact: true }],
  searchInput: ['textbox', { name: 'Search external IPs', exact: true }],
  emptyStateTitle: ['No external IPs found'],
  filteredEmptyStateTitle: ['No external IPs match your filters'],
  loadErrorTitle: ['heading', { name: 'An error occurred', exact: true }],
} as const satisfies Record<string, ExternalIpLocator>;

export const ExternalIpCreatePage = {
  pageTitle: ['heading', { name: 'Create external IP', exact: true }],
  generalStepTitle: ['heading', { name: 'General', exact: true }],
  configurationStepTitle: ['heading', { name: 'Configuration', exact: true }],
  reviewStepTitle: ['heading', { name: 'Review', exact: true }],
  reviewDetails: ['[aria-label="Review"]'],
  validationAlert: ['Fix the highlighted errors before continuing.'],
  submitError: ['Failed to create external IP'],
  name: id('metadata-name'),
  // The field id contains a dot, so a CSS id selector would read it as a class.
  project: ['[id="metadata.project"]'],
  pool: id('create-external-ip-pool'),
  next: ['button', { name: 'Next', exact: true }],
  submit: ['button', { name: 'Create external IP', exact: true }],
} as const satisfies Record<string, ExternalIpLocator>;

export const ExternalIpDeleteDialog = {
  dialog: [`[role="dialog"][aria-labelledby="${deleteResourceModalIds.title}"]`],
  title: id(deleteResourceModalIds.title),
  warning: [
    'This permanently releases the external IP back to its pool. This action cannot be undone.',
  ],
  deleteMenuItem: ['menuitem', { name: 'Delete', exact: true }],
  confirmDelete: ['button', { name: 'Delete', exact: true }],
  deleteError: ['Failed to delete external IP'],
} as const satisfies Record<string, ExternalIpLocator>;

export const ExternalIpPoolsPage = {
  pageTitle: ['heading', { name: 'External IP pools', exact: true }],
  poolsTable: ['grid', { name: 'External IP pools', exact: true }],
  createPool: ['link', { name: 'Create pool', exact: true }],
  emptyStateTitle: ['No external IP pools found'],
  deleteMenuItem: ['menuitem', { name: 'Delete', exact: true }],
  confirmDelete: ['button', { name: 'Delete', exact: true }],
  deleteError: ['Failed to delete external IP pool'],
  deleteWarning: [
    'This permanently deletes the external IP pool. Pools with allocated IPs cannot be deleted. This action cannot be undone.',
  ],
} as const satisfies Record<string, ExternalIpLocator>;

export const ExternalIpPoolCreatePage = {
  pageTitle: ['heading', { name: 'Create external IP pool', exact: true }],
  name: id('metadata-name'),
  cidr: id('external-ip-pool-cidr-0'),
  next: ['button', { name: 'Next', exact: true }],
  submit: ['button', { name: 'Create', exact: true }],
} as const satisfies Record<string, ExternalIpLocator>;
