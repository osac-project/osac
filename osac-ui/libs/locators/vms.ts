type VmsLocator =
  | readonly [role: 'link' | 'heading' | 'grid', options: { name: string; exact: true }]
  | readonly [selector: string];

export const VmsMainPage = {
  pageTitle: ['heading', { name: 'Virtual machines', exact: true }],
  vmsTable: ['grid', { name: 'Virtual machines', exact: true }],
  vmRows: ['tbody tr'],
  vmNameCell: ['td[data-label="Name"]'],
  vmStatusCell: ['td[data-label="Status"]'],
} as const satisfies Record<string, VmsLocator>;

export const VmDetailsPage = {
  pageTitle: ['h1.pf-v6-c-title'],
  statusLabel: ['.pf-v6-c-label'],
} as const satisfies Record<string, VmsLocator>;
