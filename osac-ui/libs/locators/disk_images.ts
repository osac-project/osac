import { diskImageFieldIds } from '../ui-components/src/components/DiskImage/fieldIds';
import { deleteResourceModalIds } from '../ui-components/src/components/Resource/deleteResourceModalIds';

type DiskImageLocator =
  | readonly [
      role: 'link' | 'heading' | 'button' | 'textbox' | 'grid' | 'navigation',
      options: { name: string; exact: true },
    ]
  | readonly [label: string];

const id = (value: string): readonly [string] => [`#${value}`];

export const DiskImagesMainPage = {
  pageTitle: ['heading', { name: 'Disk images', exact: true }],
  createDiskImage: ['link', { name: 'Create disk image', exact: true }],
  diskImagesTable: ['grid', { name: 'Disk images', exact: true }],
  showObsolete: id(diskImageFieldIds.showObsolete),
} as const satisfies Record<string, DiskImageLocator>;

export const DiskImageCreatePage = {
  pageTitle: ['heading', { name: 'Create disk image', exact: true }],
  name: id(diskImageFieldIds.name),
  sourceReference: id(diskImageFieldIds.sourceReference),
  guestOsFamily: id(diskImageFieldIds.guestOsFamily),
  architecture: id(diskImageFieldIds.architecture),
  scope: id(diskImageFieldIds.scope),
  create: ['button', { name: 'Create', exact: true }],
} as const satisfies Record<string, DiskImageLocator>;

export const DiskImageDetailPage = {
  deleteDialog: [`[role="dialog"][aria-labelledby="${deleteResourceModalIds.title}"]`],
  deleteTitle: id(deleteResourceModalIds.title),
  breadcrumb: ['navigation', { name: 'Breadcrumb', exact: true }],
  diskImagesBreadcrumb: ['link', { name: 'Disk images', exact: true }],
  deleteWarning: ['This permanently deletes the disk image. This action cannot be undone.'],
} as const satisfies Record<string, DiskImageLocator>;
