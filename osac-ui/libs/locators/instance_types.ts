type InstanceTypesLocator =
  | readonly [
      role: 'link' | 'button' | 'heading' | 'grid' | 'menuitem',
      options: { name: string; exact: true },
    ]
  | readonly [label: string];

export const InstanceTypesMainPage = {
  pageTitle: ['heading', { name: 'Instance types', exact: true }],
  createInstanceType: ['link', { name: 'Create instance type', exact: true }],
  instanceTypesTable: ['grid', { name: 'Instance types', exact: true }],
  instanceTypeRows: ['tbody tr'],
  instanceTypeNameCell: ['td[data-label="Name"]'],
  instanceTypeLifecycleCell: ['td[data-label="Lifecycle state"]'],
  instanceTypeVcpusCell: ['td[data-label="vCPUs"]'],
  instanceTypeMemoryGibCell: ['td[data-label="Memory (GiB)"]'],
  instanceTypeGpusCell: ['td[data-label="GPUs"]'],
  instanceTypeActionsToggle: ['button[aria-label^="Actions for "]'],
  emptyStateTitle: ['heading', { name: 'No instance types yet.', exact: true }],
} as const satisfies Record<string, InstanceTypesLocator>;

export const InstanceTypesActionsMenu = {
  deprecate: ['menuitem', { name: 'Deprecate', exact: true }],
  obsolete: ['menuitem', { name: 'Obsolete', exact: true }],
  reactivate: ['menuitem', { name: 'Reactivate', exact: true }],
  delete: ['menuitem', { name: 'Delete', exact: true }],
} as const satisfies Record<string, InstanceTypesLocator>;

export const InstanceTypesCreatePage = {
  pageTitle: ['heading', { name: 'Create instance type', exact: true }],
  instanceTypesBreadcrumb: ['button', { name: 'Instance types', exact: true }],
  name: ['Name'],
  description: ['Description'],
  vcpus: ['vCPUs'],
  vcpusPlus: ['.pf-v6-c-number-input:has(#instance-type-vcpus) button[aria-label="Plus"]'],
  memoryGib: ['Memory (GiB)'],
  memoryGibPlus: ['.pf-v6-c-number-input:has(#instance-type-memory-gib) button[aria-label="Plus"]'],
  gpuCount: ['GPU count'],
  gpuCountPlus: ['.pf-v6-c-number-input:has(#instance-type-gpu-count) button[aria-label="Plus"]'],
  gpuResourceName: ['Resource name'],
  gpuPciDeviceSelector: ['PCI device selector'],
  create: ['button', { name: 'Create', exact: true }],
  cancel: ['button', { name: 'Cancel', exact: true }],
} as const satisfies Record<string, InstanceTypesLocator>;

export const InstanceTypesDetailPage = {
  instanceTypesBreadcrumb: ['button', { name: 'Instance types', exact: true }],
  pageTitle: ['h1.pf-v6-c-title'],
  description: ['main .pf-v6-l-flex p[data-pf-content="true"]'],
  cards: ['.pf-v6-c-card'],
  detailsGroups: ['.pf-v6-c-description-list__group'],
  detailValue: ['.pf-v6-c-description-list__description'],
  noGpuMessage: ['This instance type has no GPU attached.'],
} as const satisfies Record<string, InstanceTypesLocator>;

export const InstanceTypesDeletePage = {
  deleteDialog: ['[role="dialog"][aria-labelledby="delete-confirm-title"]'],
  deleteTitle: ['#delete-confirm-title'],
  deleteWarning: ['This permanently deletes the instance type. This action cannot be undone.'],
  deleteButton: ['button', { name: 'Delete', exact: true }],
} as const satisfies Record<string, InstanceTypesLocator>;
