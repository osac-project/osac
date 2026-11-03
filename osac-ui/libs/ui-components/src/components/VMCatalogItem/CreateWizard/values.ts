import {
  type ComputeInstanceCatalogItem,
  type ComputeInstanceDiskList,
  type DiskImageReference,
  type GuestOSFamily,
  type InstanceTypeReference,
  type StorageTierReference,
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
import type { ComputeInstanceDiskValues } from '../../vm/CreateWizard/fields';

export interface ComputeCatalogItemValues extends CatalogGeneralValues {
  guestOsFamily: GuestOSFamily | undefined;
  diskImage: ProvisioningFieldValue<ResourceSelectValue>;
  instanceType: ProvisioningFieldValue<ResourceSelectValue>;
  userData: ProvisioningFieldValue<string>;
  userDataSecret: ProvisioningFieldValue<string>;
  bootDiskSizeGib: ProvisioningFieldValue<string>;
  bootDiskStorageTier: ProvisioningFieldValue<ResourceSelectValue>;
  additionalDisks: ProvisioningFieldValue<ComputeInstanceDiskValues[]>;
}

const readStringValue = (value: string | number | undefined): string =>
  value === undefined ? '' : String(value);

const readDiskListValue = (
  value: ComputeInstanceDiskList | undefined,
): ComputeInstanceDiskValues[] =>
  (value?.items ?? []).map((disk) => ({
    sizeGib: disk.sizeGib === undefined ? '' : String(disk.sizeGib),
    storageTier: resourceSelectValueFromReference(disk.storageTier),
  }));

export const emptyComputeCatalogItemValues = (): ComputeCatalogItemValues => ({
  ...catalogGeneralValues(),
  guestOsFamily: undefined,
  diskImage: provisioningFieldValue(emptyResourceSelectValue()),
  instanceType: provisioningFieldValue(emptyResourceSelectValue()),
  userData: provisioningFieldValue(''),
  userDataSecret: provisioningFieldValue(''),
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
  const diskImage = getFieldFromCatalogItemPolicy<DiskImageReference | undefined>(
    fields?.diskImage,
    undefined,
  );
  const instanceType = getFieldFromCatalogItemPolicy<InstanceTypeReference | undefined>(
    fields?.instanceType,
    undefined,
  );
  const bootDiskSizeGib = getFieldFromCatalogItemPolicy<number | undefined>(
    fields?.bootDisk?.sizeGib,
    undefined,
  );
  const bootDiskStorageTier = getFieldFromCatalogItemPolicy<StorageTierReference | undefined>(
    fields?.bootDisk?.storageTier,
    undefined,
  );
  const additionalDisks = getFieldFromCatalogItemPolicy<ComputeInstanceDiskList | undefined>(
    fields?.additionalDisks,
    undefined,
  );

  return {
    ...catalogGeneralValuesFromItem(item),
    guestOsFamily: undefined,
    diskImage: provisioningFieldValue(
      resourceSelectValueFromReference(diskImage.value),
      diskImage.locked,
    ),
    instanceType: provisioningFieldValue(
      resourceSelectValueFromReference(instanceType.value),
      instanceType.locked,
    ),
    ...catalogUserDataValuesFromPolicy(fields?.userDataSource),
    bootDiskSizeGib: provisioningFieldValue(
      readStringValue(bootDiskSizeGib.value),
      bootDiskSizeGib.locked,
    ),
    bootDiskStorageTier: provisioningFieldValue(
      resourceSelectValueFromReference(bootDiskStorageTier.value),
      bootDiskStorageTier.locked,
    ),
    additionalDisks: provisioningFieldValue(
      readDiskListValue(additionalDisks.value),
      additionalDisks.locked,
    ),
  };
};
