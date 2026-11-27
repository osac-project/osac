import { deleteResourceModalIds } from '../ui-components/src/components/Resource/deleteResourceModalIds';

type SubnetLocator =
  | readonly [
      role:
        | 'button'
        | 'columnheader'
        | 'grid'
        | 'gridcell'
        | 'heading'
        | 'link'
        | 'listbox'
        | 'menuitem'
        | 'option'
        | 'row'
        | 'textbox',
      options: { name: string; exact: true },
    ]
  | readonly [label: string]
  | readonly [selector: string];

const id = (value: string): readonly [string] => [`#${value}`];

export const SubnetsPage = {
  pageTitle: ['heading', { name: 'Subnets', exact: true }],
  subnetsTable: ['grid', { name: 'Subnets', exact: true }],
  createSubnet: ['link', { name: 'Create subnet', exact: true }],
  searchInput: ['textbox', { name: 'Search subnets by name', exact: true }],
  virtualNetworkFilter: ['button', { name: 'Filter by virtual network', exact: true }],
  emptyStateTitle: ['No subnets found'],
  filteredEmptyStateTitle: ['No subnets match your filters'],
  loadErrorTitle: ['heading', { name: 'An error occurred', exact: true }],
} as const satisfies Record<string, SubnetLocator>;

export const SubnetCreatePage = {
  pageTitle: ['heading', { name: 'Create subnet', exact: true }],
  // The FormGroup label names the MenuToggle, but its id is the stable handle.
  networkToggle: id('subnet-virtual-network'),
  name: id('metadata-name'),
  cidr: id('subnet-ipv4-cidr'),
  create: ['button', { name: 'Create', exact: true }],
  cancel: ['button', { name: 'Cancel', exact: true }],
  onlyReadyHint: ['Only ready virtual networks are listed.'],
  defaultSubnetGate: ['Available when the default subnet is Ready.'],
  submitError: ['Failed to create subnet'],
} as const satisfies Record<string, SubnetLocator>;

export const SubnetDeleteDialog = {
  dialog: [`[role="dialog"][aria-labelledby="${deleteResourceModalIds.title}"]`],
  title: id(deleteResourceModalIds.title),
  warning: ['This will permanently delete the subnet. This action cannot be undone.'],
  deleteMenuItem: ['menuitem', { name: 'Delete subnet', exact: true }],
  confirmDelete: ['button', { name: 'Delete', exact: true }],
  deleteError: ['Failed to delete subnet'],
} as const satisfies Record<string, SubnetLocator>;
