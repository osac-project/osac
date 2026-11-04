type CatalogLocator =
  | readonly [
      role: 'button' | 'heading' | 'link' | 'progressbar' | 'radio' | 'spinbutton' | 'textbox',
      options: { name: string; exact: true },
    ]
  | readonly [selector: string];

export const CatalogMainPage = {
  pageTitle: ['heading', { name: 'Catalog', exact: true }],
  createVmCatalogItem: ['link', { name: 'Create VM catalog item', exact: true }],
  catalogItemCards: ['.pf-v6-c-card'],
  catalogItemLabels: ['.pf-v6-c-card__header .pf-v6-c-label'],
  catalogItemResourceGroups: ['.pf-v6-c-description-list__group'],
  catalogItemResourceValue: ['.pf-v6-c-description-list__description'],
  catalogItemVisibility: ['.pf-v6-c-card__footer'],
  catalogLoadingIndicator: ['progressbar', { name: 'Contents', exact: true }],
} as const satisfies Record<string, CatalogLocator>;

export const CatalogCreatePage = {
  pageTitle: ['heading', { name: 'Create VM Catalog Item', exact: true }],
  globalScope: ['#catalog-item-scope-global'],
  name: ['textbox', { name: 'Name', exact: true }],
  description: ['textbox', { name: 'Description', exact: true }],
  template: ['button', { name: 'Provisioning template', exact: true }],
  guestOsLinux: ['radio', { name: 'Linux', exact: true }],
  diskImage: ['button', { name: 'Select the image reference', exact: true }],
  instanceType: ['button', { name: 'Instance type', exact: true }],
  bootDiskSize: ['spinbutton', { name: 'Boot disk size', exact: true }],
  review: ['heading', { name: 'Review', exact: true }],
  next: ['button', { name: 'Next', exact: true }],
  create: ['button', { name: 'Create', exact: true }],
  publish: ['button', { name: 'Publish', exact: true }],
  unpublished: ['button', { name: 'Unpublish', exact: true }],
} as const satisfies Record<string, CatalogLocator>;

export const CatalogDeletePage = {
  dialog: ['[role="dialog"][aria-labelledby="delete-confirm-title"]'],
  dialogTitle: ['#delete-confirm-title'],
  warning: [
    'This permanently deletes the catalog item. It does not affect resources that tenants have ' +
      'already provisioned from it.',
  ],
  delete: ['button', { name: 'Delete', exact: true }],
} as const satisfies Record<string, CatalogLocator>;
