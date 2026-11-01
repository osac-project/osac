import {
  type BareMetalInstanceCatalogItem,
  type BareMetalInstanceTypeReference,
  type DiskImageReference,
  GuestOSFamily,
} from '@osac/types';

import {
  type ResourceSelectValue,
  emptyResourceSelectValue,
  resourceSelectValueFromReference,
} from '../../../Form/resourceSelectValue';
import {
  type ProvisioningFieldValue,
  getFieldFromCatalogItemPolicy,
  provisioningFieldValue,
} from '../../../ProvisioningWizard/fields/provisioningFieldValue';
import {
  type CatalogGeneralValues,
  catalogGeneralValues,
  catalogGeneralValuesFromItem,
} from '../CatalogGeneralStep';
import { catalogUserDataValuesFromPolicy } from '../catalogUserData';

export interface BareMetalCatalogItemValues extends CatalogGeneralValues {
  guestOsFamily: GuestOSFamily;
  instanceType: ProvisioningFieldValue<string>;
  diskImage: ProvisioningFieldValue<ResourceSelectValue>;
  userData: ProvisioningFieldValue<string>;
}

const readInstanceTypeValue = (value: BareMetalInstanceTypeReference | undefined): string =>
  value?.name || value?.id || '';

export const emptyBareMetalCatalogItemValues = (): BareMetalCatalogItemValues => ({
  ...catalogGeneralValues(),
  guestOsFamily: GuestOSFamily.GUEST_OS_FAMILY_LINUX,
  instanceType: provisioningFieldValue(''),
  diskImage: provisioningFieldValue(emptyResourceSelectValue()),
  userData: provisioningFieldValue(''),
});

export const getBareMetalCatalogItemInitialValues = (
  item?: BareMetalInstanceCatalogItem,
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
    guestOsFamily: GuestOSFamily.GUEST_OS_FAMILY_LINUX,
    instanceType: provisioningFieldValue(
      readInstanceTypeValue(instanceType.value),
      instanceType.locked,
    ),
    diskImage: provisioningFieldValue(
      resourceSelectValueFromReference(diskImage.value),
      diskImage.locked,
    ),
    ...catalogUserDataValuesFromPolicy(fields?.userDataSource),
  };
};
