import type { MessageInitShape } from '@bufbuild/protobuf';

import { type ComputeInstanceCatalogItem, ComputeInstanceCatalogItemSchema } from '@osac/types';

import type { ComputeCatalogItemValues } from './values';
import { buildUpdateMaskPaths } from '../../../api/v1/update-mask';
import { catalogGeneralItemPayload } from '../../CatalogItem/CatalogGeneralStep';
import {
  catalogFieldPolicyClearPath,
  catalogFieldPolicyFromValue,
} from '../../CatalogItem/catalogItemPolicy';
import { catalogUserDataPolicyFromValues } from '../../CatalogItem/catalogUserData';
import { type ResourceSelectValue } from '../../Form/resourceSelectValue';
import type { ComputeInstanceDiskValues } from '../../vm/CreateWizard/fields';

type CatalogItemFieldsInit = NonNullable<
  MessageInitShape<typeof ComputeInstanceCatalogItemSchema>['fields']
>;

const isEmptyString = (value: string) => !value.trim();
const isEmptyResource = (value: ResourceSelectValue) => !value.id;
const isEmptyDiskList = (value: ComputeInstanceDiskValues[]) => !value.length;

const toTrimmedString = (value: string) => value.trim();

const toDiskListRef = (value: ComputeInstanceDiskValues[]) => ({
  items: value
    .filter((disk) => disk.sizeGib.trim())
    .map((disk) => ({
      sizeGib: Number(disk.sizeGib.trim()),
      ...(disk.storageTier.id ? { storageTier: disk.storageTier } : {}),
    })),
});

const buildComputeCatalogItemFieldsPayload = (
  values: ComputeCatalogItemValues,
): CatalogItemFieldsInit => {
  const fields: CatalogItemFieldsInit = {};

  const diskImage = catalogFieldPolicyFromValue(values.diskImage, isEmptyResource);
  if (diskImage) {
    fields.diskImage = diskImage;
  }

  const instanceType = catalogFieldPolicyFromValue(values.instanceType, isEmptyResource);
  if (instanceType) {
    fields.instanceType = instanceType;
  }

  const userDataSource = catalogUserDataPolicyFromValues(values);
  if (userDataSource) {
    fields.userDataSource = userDataSource;
  }

  const bootDiskSizeGib = catalogFieldPolicyFromValue(
    values.bootDiskSizeGib,
    isEmptyString,
    (value) => Number(toTrimmedString(value)),
  );
  const bootDiskStorageTier = catalogFieldPolicyFromValue(
    values.bootDiskStorageTier,
    isEmptyResource,
  );
  if (bootDiskSizeGib || bootDiskStorageTier) {
    fields.bootDisk = {
      ...(bootDiskSizeGib ? { sizeGib: bootDiskSizeGib } : {}),
      ...(bootDiskStorageTier ? { storageTier: bootDiskStorageTier } : {}),
    };
  }

  const additionalDisks = catalogFieldPolicyFromValue(
    values.additionalDisks,
    isEmptyDiskList,
    toDiskListRef,
  );
  if (additionalDisks) {
    fields.additionalDisks = additionalDisks;
  }

  return fields;
};

export const buildComputeCatalogItemCreatePayload = (
  values: ComputeCatalogItemValues,
): MessageInitShape<typeof ComputeInstanceCatalogItemSchema> => ({
  metadata: catalogGeneralItemPayload(values),
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
  _item: ComputeInstanceCatalogItem,
  values: ComputeCatalogItemValues,
): MessageInitShape<typeof ComputeInstanceCatalogItemSchema> => ({
  fields: buildComputeCatalogItemFieldsPayload(values),
});

export const buildComputeCatalogItemUpdateRequest = (
  item: ComputeInstanceCatalogItem,
  values: ComputeCatalogItemValues,
) => {
  const payload = buildComputeCatalogItemUpdatePayload(item, values);
  const object = { id: item.id, ...payload };
  const fields = payload.fields;
  const clearPaths = [
    catalogFieldPolicyClearPath(item.fields?.diskImage, fields?.diskImage, 'fields.disk_image'),
    catalogFieldPolicyClearPath(
      item.fields?.instanceType,
      fields?.instanceType,
      'fields.instance_type',
    ),
    catalogFieldPolicyClearPath(
      item.fields?.userDataSource,
      fields?.userDataSource,
      'fields.user_data_source',
    ),
    catalogFieldPolicyClearPath(
      item.fields?.bootDisk?.sizeGib,
      fields?.bootDisk?.sizeGib,
      'fields.boot_disk.size_gib',
    ),
    catalogFieldPolicyClearPath(
      item.fields?.bootDisk?.storageTier,
      fields?.bootDisk?.storageTier,
      'fields.boot_disk.storage_tier',
    ),
    catalogFieldPolicyClearPath(
      item.fields?.additionalDisks,
      fields?.additionalDisks,
      'fields.additional_disks',
    ),
  ].flat();

  return {
    object,
    updateMask: {
      paths: [
        ...new Set([
          ...buildUpdateMaskPaths(object, { schema: ComputeInstanceCatalogItemSchema }),
          ...clearPaths,
        ]),
      ],
    },
  };
};
