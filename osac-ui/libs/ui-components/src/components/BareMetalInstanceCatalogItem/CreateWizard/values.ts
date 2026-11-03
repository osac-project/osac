import {
  type BareMetalInstanceCatalogItem,
  type BareMetalInstanceTypeReference,
  type DiskImageReference,
  type GuestOSFamily,
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
  guestOsFamily: GuestOSFamily | undefined;
  instanceType: ProvisioningFieldValue<ResourceSelectValue>;
  diskImage: ProvisioningFieldValue<ResourceSelectValue>;
  userData: ProvisioningFieldValue<string>;
  userDataSecret: ProvisioningFieldValue<string>;
}

export const emptyBareMetalCatalogItemValues = (): BareMetalCatalogItemValues => ({
  ...catalogGeneralValues(),
  guestOsFamily: undefined,
  instanceType: provisioningFieldValue(emptyResourceSelectValue()),
  diskImage: provisioningFieldValue(emptyResourceSelectValue()),
  userData: provisioningFieldValue(''),
  userDataSecret: provisioningFieldValue(''),
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
    guestOsFamily: undefined,
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
