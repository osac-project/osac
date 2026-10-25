import { type BareMetalInstanceCatalogItem, GuestOSFamily } from '@osac/types';

import {
  type ResourceSelectValue,
  emptyResourceSelectValue,
} from '../../../Form/resourceSelectValue';
import {
  type ProvisioningFieldValue,
  provisioningFieldValue,
} from '../../../ProvisioningWizard/fields/provisioningFieldValue';
import {
  type CatalogGeneralValues,
  catalogGeneralValues,
  catalogGeneralValuesFromItem,
} from '../CatalogGeneralStep';
import { catalogValueFromPolicy } from '../catalogItemPolicy';

export interface BareMetalCatalogItemValues extends CatalogGeneralValues {
  guestOsFamily: GuestOSFamily;
  instanceType: ProvisioningFieldValue<string>;
  diskImage: ProvisioningFieldValue<ResourceSelectValue>;
  userDataSource: ProvisioningFieldValue<'inline' | 'secret'>;
  userData: ProvisioningFieldValue<string>;
  userDataSecret: ProvisioningFieldValue<string>;
}

const readInstanceTypeValue = (value: unknown): string => {
  const reference = value as { id?: string; name?: string } | undefined;
  return reference?.name?.trim() || reference?.id?.trim() || '';
};

const readResourceReference = (value: unknown): ResourceSelectValue => {
  const reference = value as { id?: string; name?: string } | undefined;
  return reference?.id || reference?.name
    ? { id: reference.id ?? '', name: reference.name ?? '' }
    : emptyResourceSelectValue();
};

const readStringValue = (value: unknown): string => {
  if (typeof value === 'string') {
    return value;
  }
  return '';
};

export const emptyBareMetalCatalogItemValues = (): BareMetalCatalogItemValues => ({
  ...catalogGeneralValues(),
  guestOsFamily: GuestOSFamily.GUEST_OS_FAMILY_LINUX,
  instanceType: provisioningFieldValue(''),
  diskImage: provisioningFieldValue(emptyResourceSelectValue()),
  userDataSource: provisioningFieldValue('inline'),
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

  return {
    ...catalogGeneralValuesFromItem(item),
    guestOsFamily: GuestOSFamily.GUEST_OS_FAMILY_LINUX,
    instanceType: catalogValueFromPolicy(fields?.instanceType, readInstanceTypeValue, ''),
    diskImage: catalogValueFromPolicy(
      fields?.diskImage,
      readResourceReference,
      emptyResourceSelectValue(),
    ),
    // Gap: catalog-item serialization currently exposes only inline user data,
    // so the shared UserDataField source and secret reference cannot be restored.
    userDataSource: provisioningFieldValue('inline'),
    userData: catalogValueFromPolicy(fields?.userData, readStringValue, ''),
    userDataSecret: provisioningFieldValue(''),
  };
};
