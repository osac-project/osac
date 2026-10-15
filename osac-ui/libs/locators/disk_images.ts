type DiskImageLocator =
  | readonly [
      role: 'link' | 'heading' | 'button' | 'textbox' | 'grid' | 'navigation',
      options: { name: string; exact: true },
    ]
  | readonly [label: string];

export const DiskImagesMainPage = {
  pageTitle: ['heading', { name: 'Disk images', exact: true }],
  createDiskImage: ['link', { name: 'Create disk image', exact: true }],
  diskImagesTable: ['grid', { name: 'Disk images', exact: true }],
} as const satisfies Record<string, DiskImageLocator>;

export const DiskImageCreatePage = {
  pageTitle: ['heading', { name: 'Create disk image', exact: true }],
  name: ['Name'],
  sourceReference: ['Source reference'],
  guestOsFamily: ['button', { name: 'Guest OS family', exact: true }],
  architecture: ['button', { name: 'Architecture', exact: true }],
  scope: ['button', { name: 'Scope', exact: true }],
  create: ['button', { name: 'Create', exact: true }],
} as const satisfies Record<string, DiskImageLocator>;

export const DiskImageDetailPage = {
  deleteDialog: ['[role="dialog"][aria-labelledby="delete-confirm-title"]'],
  deleteTitle: ['#delete-confirm-title'],
  breadcrumb: ['navigation', { name: 'Breadcrumb', exact: true }],
  diskImagesBreadcrumb: ['link', { name: 'Disk images', exact: true }],
  deleteWarning: ['This permanently deletes the disk image. This action cannot be undone.'],
} as const satisfies Record<string, DiskImageLocator>;
