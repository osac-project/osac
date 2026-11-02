import {
  type BareMetalInstanceCatalogItem,
  type BareMetalInstanceTypeReference,
  type DiskImage,
  type DiskImageReference,
  GuestOSFamily,
} from '@osac/types';

import {
  type CatalogGeneralValues,
  catalogGeneralValues,
  catalogGeneralValuesFromItem,
} from '../../CatalogItem/CatalogGeneralStep';
import { catalogUserDataValuesFromPolicy } from '../../CatalogItem/catalogUserData';
import {
  type ResourceSelectValue,
  emptyResourceSelectValue,
  resourceSelectValueFromReference,
} from '../../Form/resourceSelectValue';
import {
  type ProvisioningFieldValue,
  getFieldFromCatalogItemPolicy,
  provisioningFieldValue,
} from '../../ProvisioningWizard/fields/provisioningFieldValue';

export interface BareMetalCatalogItemValues extends CatalogGeneralValues {
  guestOsFamily: GuestOSFamily;
  instanceType: ProvisioningFieldValue<ResourceSelectValue>;
  diskImage: ProvisioningFieldValue<ResourceSelectValue>;
  userData: ProvisioningFieldValue<string>;
  userDataSecret: ProvisioningFieldValue<string>;
}

export const emptyBareMetalCatalogItemValues = (): BareMetalCatalogItemValues => ({
  ...catalogGeneralValues(),
  guestOsFamily: GuestOSFamily.GUEST_OS_FAMILY_LINUX,
  instanceType: provisioningFieldValue(emptyResourceSelectValue()),
  diskImage: provisioningFieldValue(emptyResourceSelectValue()),
  userData: provisioningFieldValue(''),
  userDataSecret: provisioningFieldValue(''),
});

export const getBareMetalCatalogItemDiskImageId = (
  item?: BareMetalInstanceCatalogItem,
): string | undefined => {
  const diskImage = getFieldFromCatalogItemPolicy<DiskImageReference | undefined>(
    item?.fields?.diskImage,
    undefined,
  );
  return diskImage.value?.id || undefined;
};

export const getBareMetalCatalogItemInitialValues = (
  item?: BareMetalInstanceCatalogItem,
  diskImageResource?: DiskImage,
): BareMetalCatalogItemValues => {
  if (!item) {
    return emptyBareMetalCatalogItemValues();
  }

  const fields = item.fields;
  const instanceType = getFieldFromCatalogItemPolicy<BareMetalInstanceTypeReference | undefined>(
    fields?.instanceType,
    undefined,
  );
  const diskImage = getFieldFromCatalogItemPolicy<DiskImageReference | undefined>(
    fields?.diskImage,
    undefined,
  );

  return {
    ...catalogGeneralValuesFromItem(item),
    guestOsFamily: diskImage.value?.id
      ? (diskImageResource?.spec?.guestOsFamily ?? GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED)
      : GuestOSFamily.GUEST_OS_FAMILY_LINUX,
    instanceType: provisioningFieldValue(
      resourceSelectValueFromReference(instanceType.value),
      instanceType.locked,
    ),
    diskImage: provisioningFieldValue(
      resourceSelectValueFromReference(diskImage.value),
      diskImage.locked,
    ),
    ...catalogUserDataValuesFromPolicy(fields?.userDataSource),
  };
};
