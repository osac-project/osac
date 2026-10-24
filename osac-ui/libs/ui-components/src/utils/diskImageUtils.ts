import {
  BareMetalInstance,
  BareMetalInstanceCatalogItem,
  ComputeInstance,
  ComputeInstanceCatalogItem,
  DiskImage,
  DiskImageReference,
  GuestOSFamily,
} from '@osac/types';
import { bareMetalDiskImageReferenceFromPolicy } from '@osac/ui-components/components/catalog/bareMetalCatalogItemResourceDisplay';
import type { OsType } from '@osac/ui-components/components/shared/GuestOsIcon';

export const diskImageLabelFromReference = (
  diskImage: DiskImage | undefined,
  reference: DiskImageReference | undefined,
): string => {
  return diskImage?.metadata?.name || diskImage?.metadata?.displayName || reference?.name || '—';
};

export const findDiskImageReference = (
  instance: BareMetalInstance | ComputeInstance,
  catalogItems?: BareMetalInstanceCatalogItem[] | ComputeInstanceCatalogItem[],
): DiskImageReference | undefined => {
  const reference = instance.spec?.diskImage;
  if (reference) {
    return reference;
  }
  const catalogReference = instance.spec?.catalogItem;
  if (catalogReference) {
    const catalogItem = catalogItems?.find(
      (item) =>
        (catalogReference.id && item.id === catalogReference.id) ||
        (catalogReference.name && item.metadata?.name === catalogReference.name),
    );

    if (catalogItem) {
      return bareMetalDiskImageReferenceFromPolicy(catalogItem.fields?.diskImage);
    }
  }

  return undefined;
};

export const findDiskImageForReference = (
  diskImages: DiskImage[],
  reference: DiskImageReference | undefined,
): DiskImage | undefined => {
  if (!reference) {
    return undefined;
  }
  if (reference.id) {
    const byId = diskImages.find((diskImage) => diskImage.id === reference.id);
    if (byId) {
      return byId;
    }
  }
  if (reference.name) {
    return diskImages.find(
      (diskImage) =>
        diskImage.metadata?.name === reference.name &&
        diskImage.metadata?.project === reference.project,
    );
  }
  return undefined;
};

export const diskImageGuestOsIconType = (diskImage: DiskImage | undefined): OsType | undefined => {
  if (!diskImage) {
    return undefined;
  }
  if (diskImage.spec?.guestOsFamily === GuestOSFamily.GUEST_OS_FAMILY_WINDOWS) {
    return 'windows';
  }
  const name = (diskImage.metadata?.name ?? diskImage.metadata?.displayName ?? '').toLowerCase();
  if (name.includes('rhel') || name.includes('red hat')) {
    return 'rhel';
  }
  return 'linux';
};
