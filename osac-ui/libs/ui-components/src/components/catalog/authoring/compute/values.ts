import type { ComputeInstanceCatalogItem } from '@osac/types';

import {
  type ResourceSelectValue,
  emptyResourceSelectValue,
} from '../../../Form/resourceSelectValue';
import {
  type ProvisioningFieldValue,
  provisioningFieldValue,
} from '../../../ProvisioningWizard/fields/provisioningFieldValue';
import type { ComputeInstanceDiskValues } from '../../../vm/CreateWizard/fields';
import {
  type CatalogGeneralValues,
  catalogGeneralValues,
  catalogGeneralValuesFromItem,
} from '../CatalogGeneralStep';
import { catalogValueFromPolicy } from '../catalogItemPolicy';

export interface ComputeCatalogItemValues extends CatalogGeneralValues {
  diskImage: ProvisioningFieldValue<ResourceSelectValue>;
  instanceType: ProvisioningFieldValue<ResourceSelectValue>;
  userData: ProvisioningFieldValue<string>;
  bootDiskSizeGib: ProvisioningFieldValue<string>;
  bootDiskStorageTier: ProvisioningFieldValue<ResourceSelectValue>;
  additionalDisks: ProvisioningFieldValue<ComputeInstanceDiskValues[]>;
}

const readResourceReference = (value: unknown): ResourceSelectValue => {
  const ref = value as { id?: string; name?: string } | undefined;
  return ref?.id || ref?.name
    ? { id: ref.id ?? '', name: ref.name ?? '' }
    : emptyResourceSelectValue();
};

const readStringValue = (value: unknown): string => {
  if (typeof value === 'string') {
    return value;
  }
  return typeof value === 'number' ? String(value) : '';
};

const readDiskListValue = (value: unknown): ComputeInstanceDiskValues[] => {
  const items = (
    value as { items?: { sizeGib?: number; storageTier?: { id?: string; name?: string } }[] }
  )?.items;
  return (items ?? []).map((disk) => ({
    sizeGib: disk.sizeGib === undefined ? '' : String(disk.sizeGib),
    storageTier: disk.storageTier
      ? readResourceReference(disk.storageTier)
      : emptyResourceSelectValue(),
  }));
};

export const emptyComputeCatalogItemValues = (): ComputeCatalogItemValues => ({
  ...catalogGeneralValues(),
  diskImage: provisioningFieldValue(emptyResourceSelectValue()),
  instanceType: provisioningFieldValue(emptyResourceSelectValue()),
  userData: provisioningFieldValue(''),
  bootDiskSizeGib: provisioningFieldValue(''),
  bootDiskStorageTier: provisioningFieldValue(emptyResourceSelectValue()),
  additionalDisks: provisioningFieldValue([]),
});

export const getComputeCatalogItemInitialValues = (
  item?: ComputeInstanceCatalogItem,
): ComputeCatalogItemValues => {
  if (!item) {
    return emptyComputeCatalogItemValues();
  }

  const fields = item.fields;

  return {
    ...catalogGeneralValuesFromItem(item),
    diskImage: catalogValueFromPolicy(
      fields?.diskImage,
      readResourceReference,
      emptyResourceSelectValue(),
    ),
    instanceType: catalogValueFromPolicy(
      fields?.instanceType,
      readResourceReference,
      emptyResourceSelectValue(),
    ),
    userData: catalogValueFromPolicy(fields?.userData, readStringValue, ''),
    bootDiskSizeGib: catalogValueFromPolicy(fields?.bootDisk?.sizeGib, readStringValue, ''),
    bootDiskStorageTier: catalogValueFromPolicy(
      fields?.bootDisk?.storageTier,
      readResourceReference,
      emptyResourceSelectValue(),
    ),
    additionalDisks: catalogValueFromPolicy(fields?.additionalDisks, readDiskListValue, []),
  };
};
