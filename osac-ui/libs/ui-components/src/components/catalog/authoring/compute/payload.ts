import type { MessageInitShape } from '@bufbuild/protobuf';

import { type ComputeInstanceCatalogItem, ComputeInstanceCatalogItemSchema } from '@osac/types';

import type { ComputeCatalogItemValues } from './values';
import { buildUpdateMaskPaths } from '../../../../api/v1/update-mask';
import { type ResourceSelectValue } from '../../../Form/resourceSelectValue';
import type { ComputeInstanceDiskValues } from '../../../vm/CreateWizard/fields';
import { catalogGeneralItemPayload } from '../CatalogGeneralStep';
import { catalogFieldPolicyClearPath, catalogFieldPolicyFromValue } from '../catalogItemPolicy';
import { catalogUserDataPolicyFromValues } from '../catalogUserData';

type CatalogItemFieldsInit = NonNullable<
  MessageInitShape<typeof ComputeInstanceCatalogItemSchema>['fields']
>;
type BootDiskFieldsInit = NonNullable<CatalogItemFieldsInit['bootDisk']>;
type FieldPolicyInit<K extends keyof CatalogItemFieldsInit> = NonNullable<CatalogItemFieldsInit[K]>;

const isEmptyString = (value: string) => !value.trim();
const isEmptyResource = (value: ResourceSelectValue) => !value.id.trim();
const isEmptyDiskList = (value: ComputeInstanceDiskValues[]) => !value.length;

const toResourceRef = (value: ResourceSelectValue) => ({ id: value.id.trim() });
const toTrimmedString = (value: string) => value.trim();

const toDiskListRef = (value: ComputeInstanceDiskValues[]) => ({
  items: value
    .filter((disk) => disk.sizeGib.trim())
    .map((disk) => ({
      sizeGib: Number(disk.sizeGib.trim()),
      ...(disk.storageTier.id.trim() ? { storageTier: { id: disk.storageTier.id.trim() } } : {}),
    })),
});

const buildComputeCatalogItemFieldsPayload = (
  values: ComputeCatalogItemValues,
): CatalogItemFieldsInit => {
  const fields: CatalogItemFieldsInit = {};

  const diskImage = catalogFieldPolicyFromValue<
    ResourceSelectValue,
    ReturnType<typeof toResourceRef>,
    FieldPolicyInit<'diskImage'>
  >(values.diskImage, isEmptyResource, toResourceRef);
  if (diskImage) {
    fields.diskImage = diskImage;
  }

  const instanceType = catalogFieldPolicyFromValue<
    ResourceSelectValue,
    ReturnType<typeof toResourceRef>,
    FieldPolicyInit<'instanceType'>
  >(values.instanceType, isEmptyResource, toResourceRef);
  if (instanceType) {
    fields.instanceType = instanceType;
  }

  const userDataSource = catalogUserDataPolicyFromValues(values);
  if (userDataSource) {
    fields.userDataSource = userDataSource;
  }

  const bootDiskSizeGib = catalogFieldPolicyFromValue<
    string,
    number,
    NonNullable<BootDiskFieldsInit['sizeGib']>
  >(values.bootDiskSizeGib, isEmptyString, (value) => Number(toTrimmedString(value)));
  const bootDiskStorageTier = catalogFieldPolicyFromValue<
    ResourceSelectValue,
    ReturnType<typeof toResourceRef>,
    NonNullable<BootDiskFieldsInit['storageTier']>
  >(values.bootDiskStorageTier, isEmptyResource, toResourceRef);
  if (bootDiskSizeGib || bootDiskStorageTier) {
    fields.bootDisk = {
      ...(bootDiskSizeGib ? { sizeGib: bootDiskSizeGib } : {}),
      ...(bootDiskStorageTier ? { storageTier: bootDiskStorageTier } : {}),
    };
  }

  const additionalDisks = catalogFieldPolicyFromValue<
    ComputeInstanceDiskValues[],
    ReturnType<typeof toDiskListRef>,
    FieldPolicyInit<'additionalDisks'>
  >(values.additionalDisks, isEmptyDiskList, toDiskListRef);
  if (additionalDisks) {
    fields.additionalDisks = additionalDisks;
  }

  return fields;
};

export const buildComputeCatalogItemCreatePayload = (
  values: ComputeCatalogItemValues,
): MessageInitShape<typeof ComputeInstanceCatalogItemSchema> => ({
  metadata: catalogGeneralItemPayload(values),
  template: { id: values.template.id.trim() },
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
