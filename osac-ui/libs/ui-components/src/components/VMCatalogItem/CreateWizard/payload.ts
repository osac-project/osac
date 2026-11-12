import type { MessageInitShape } from '@bufbuild/protobuf';

import { type ComputeInstanceCatalogItem, ComputeInstanceCatalogItemSchema } from '@osac/types';

import type { ComputeCatalogItemValues } from './values';
import { catalogGeneralItemPayload } from '../../CatalogItem/CatalogGeneralStep';
import { catalogFieldPolicyFromValue } from '../../CatalogItem/catalogItemPolicy';
import { catalogUserDataPolicyFromValues } from '../../CatalogItem/catalogUserData';
import { type ResourceSelectValue } from '../../Form/resourceSelectValue';
import type { ComputeInstanceDiskValues } from '../../vm/CreateWizard/fields';

type CatalogItemFieldsInit = NonNullable<
  MessageInitShape<typeof ComputeInstanceCatalogItemSchema>['fields']
>;

const isEmptyResource = (value: ResourceSelectValue) => !value.id;
const isEmptyDiskList = (value: ComputeInstanceDiskValues[]) => !value.length;

const toDiskListRef = (value: ComputeInstanceDiskValues[]) => ({
  items: value.map((disk) => ({
    sizeGib: disk.sizeGib,
    ...(disk.storageTier.id ? { storageTier: disk.storageTier } : {}),
  })),
});

const buildComputeCatalogItemFieldsPayload = (
  values: ComputeCatalogItemValues,
  item?: ComputeInstanceCatalogItem,
): CatalogItemFieldsInit => {
  const fields: CatalogItemFieldsInit = {};

  const diskImage = catalogFieldPolicyFromValue(values.diskImage, isEmptyResource);
  if (diskImage) {
    fields.diskImage = diskImage;
  } else if (item?.fields?.diskImage) {
    fields.diskImage = undefined;
  }

  const instanceType = catalogFieldPolicyFromValue(values.instanceType, isEmptyResource);
  if (instanceType) {
    fields.instanceType = instanceType;
  } else if (item?.fields?.instanceType) {
    fields.instanceType = undefined;
  }

  const userDataSource = catalogUserDataPolicyFromValues(values);
  if (userDataSource) {
    fields.userDataSource = userDataSource;
  } else if (item?.fields?.userDataSource) {
    fields.userDataSource = undefined;
  }

  const bootDiskSizeValue = values.bootDiskSizeGib.value;
  const bootDiskSizeGib =
    bootDiskSizeValue === undefined
      ? undefined
      : catalogFieldPolicyFromValue(
          { value: bootDiskSizeValue, locked: values.bootDiskSizeGib.locked },
          () => false,
        );
  const bootDiskStorageTier = catalogFieldPolicyFromValue(
    values.bootDiskStorageTier,
    isEmptyResource,
  );
  if (bootDiskSizeGib || bootDiskStorageTier) {
    fields.bootDisk = {
      ...(bootDiskSizeGib ? { sizeGib: bootDiskSizeGib } : {}),
      ...(!bootDiskSizeGib && item?.fields?.bootDisk?.sizeGib ? { sizeGib: undefined } : {}),
      ...(bootDiskStorageTier ? { storageTier: bootDiskStorageTier } : {}),
      ...(!bootDiskStorageTier && item?.fields?.bootDisk?.storageTier
        ? { storageTier: undefined }
        : {}),
    };
  } else if (item?.fields?.bootDisk) {
    fields.bootDisk = { sizeGib: undefined, storageTier: undefined };
  }

  const additionalDisks = catalogFieldPolicyFromValue(
    values.additionalDisks,
    isEmptyDiskList,
    toDiskListRef,
  );
  if (additionalDisks) {
    fields.additionalDisks = additionalDisks;
  } else if (item?.fields?.additionalDisks) {
    fields.additionalDisks = undefined;
  }

  return fields;
};

export const buildComputeCatalogItemCreatePayload = (
  values: ComputeCatalogItemValues,
): MessageInitShape<typeof ComputeInstanceCatalogItemSchema> => ({
  metadata: catalogGeneralItemPayload(values),
  description: values.description,
  template: values.template,
  published: false,
  fields: buildComputeCatalogItemFieldsPayload(values),
});

/**
 * Builds the fields sent on update. `metadata.name`, `metadata.tenant`,
 * `metadata.project`, and `template` are never included: the general step
 * disables those controls once editing, so they are omitted here too and the
 * update mask (built from this object's own keys) cannot touch them. The caller
 * adds `id` when assembling the update request, matching the other wizards'
 * convention.
 */
export const buildComputeCatalogItemUpdatePayload = (
  item: ComputeInstanceCatalogItem,
  values: ComputeCatalogItemValues,
): MessageInitShape<typeof ComputeInstanceCatalogItemSchema> => ({
  description: values.description,
  fields: buildComputeCatalogItemFieldsPayload(values, item),
});

export const buildComputeCatalogItemUpdateRequest = (
  item: ComputeInstanceCatalogItem,
  values: ComputeCatalogItemValues,
) => {
  const payload = buildComputeCatalogItemUpdatePayload(item, values);
  return { object: { id: item.id, ...payload } };
};
