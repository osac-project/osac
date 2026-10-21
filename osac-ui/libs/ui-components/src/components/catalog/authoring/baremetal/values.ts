import type { BareMetalInstanceCatalogItem } from '@osac/types';

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
  instanceType: ProvisioningFieldValue<string>;
  diskImage: ProvisioningFieldValue<ResourceSelectValue>;
  userData: ProvisioningFieldValue<string>;
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

  return {
    ...catalogGeneralValuesFromItem(item),
    instanceType: catalogValueFromPolicy(fields?.instanceType, readInstanceTypeValue, ''),
    diskImage: catalogValueFromPolicy(
      fields?.diskImage,
      readResourceReference,
      emptyResourceSelectValue(),
    ),
    userData: catalogValueFromPolicy(fields?.userData, readStringValue, ''),
  };
};
