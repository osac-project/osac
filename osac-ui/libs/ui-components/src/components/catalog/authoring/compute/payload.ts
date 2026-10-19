import type { MessageInitShape } from '@bufbuild/protobuf';

import { type ComputeInstanceCatalogItem, ComputeInstanceCatalogItemSchema } from '@osac/types';

import type { ComputeCatalogItemValues } from './values';
import { type ResourceSelectValue } from '../../../Form/resourceSelectValue';
import type { ProvisioningFieldValue } from '../../../ProvisioningWizard/fields/provisioningFieldValue';
import type { ComputeInstanceDiskValues } from '../../../vm/CreateWizard/fields';
import { catalogGeneralItemPayload } from '../CatalogGeneralStep';
import { policyFromCatalogValue } from '../catalogItemPolicy';

type CatalogItemFieldsInit = NonNullable<
  MessageInitShape<typeof ComputeInstanceCatalogItemSchema>['fields']
>;
type BootDiskFieldsInit = NonNullable<CatalogItemFieldsInit['bootDisk']>;
type FieldPolicyInit<K extends keyof CatalogItemFieldsInit> = NonNullable<CatalogItemFieldsInit[K]>;

const isEmptyString = (value: unknown) => typeof value !== 'string' || !value.trim();
const isEmptyResource = (value: unknown) => !(value as ResourceSelectValue | undefined)?.id?.trim();
const isEmptyDiskList = (value: unknown) =>
  !(value as ComputeInstanceDiskValues[] | undefined)?.length;

const toResourceRef = (value: unknown) => ({ id: (value as ResourceSelectValue).id.trim() });
const toTrimmedString = (value: unknown) => (value as string).trim();

const toDiskListRef = (value: unknown) => ({
  items: (value as ComputeInstanceDiskValues[])
    .filter((disk) => disk.sizeGib.trim())
    .map((disk) => ({
      sizeGib: Number(disk.sizeGib.trim()),
      ...(disk.storageTier.id.trim() ? { storageTier: { id: disk.storageTier.id.trim() } } : {}),
    })),
});

/**
 * Encodes a `{ value, locked }` field into a `*FieldPolicy` init shape via
 * `policyFromCatalogValue`. The cast is unavoidable: `policyFromCatalogValue` is
 * generic so the shared decode/encode contract in `catalogItemPolicy.ts` stays
 * untyped against any one generated message, and TypeScript cannot verify a fresh
 * object literal against an unconstrained type parameter.
 */
const buildFieldPolicy = <T>(
  field: ProvisioningFieldValue<unknown>,
  isEmpty: (value: unknown) => boolean,
  toValue: (value: unknown) => unknown,
): T | undefined =>
  policyFromCatalogValue<T>(
    field,
    isEmpty,
    (value) => ({ behavior: { case: 'locked', value: toValue(value) } }) as T,
    (value) => ({ behavior: { case: 'editable', value: { defaultValue: toValue(value) } } }) as T,
  );

const buildComputeCatalogItemFieldsPayload = (
  values: ComputeCatalogItemValues,
): CatalogItemFieldsInit => {
  const fields: CatalogItemFieldsInit = {};

  const diskImage = buildFieldPolicy<FieldPolicyInit<'diskImage'>>(
    values.diskImage,
    isEmptyResource,
    toResourceRef,
  );
  if (diskImage) {
    fields.diskImage = diskImage;
  }

  const instanceType = buildFieldPolicy<FieldPolicyInit<'instanceType'>>(
    values.instanceType,
    isEmptyResource,
    toResourceRef,
  );
  if (instanceType) {
    fields.instanceType = instanceType;
  }

  const userData = buildFieldPolicy<FieldPolicyInit<'userData'>>(
    values.userData,
    isEmptyString,
    toTrimmedString,
  );
  if (userData) {
    fields.userData = userData;
  }

  const bootDiskSizeGib = buildFieldPolicy<NonNullable<BootDiskFieldsInit['sizeGib']>>(
    values.bootDiskSizeGib,
    isEmptyString,
    (value) => Number(toTrimmedString(value)),
  );
  const bootDiskStorageTier = buildFieldPolicy<NonNullable<BootDiskFieldsInit['storageTier']>>(
    values.bootDiskStorageTier,
    isEmptyResource,
    toResourceRef,
  );
  if (bootDiskSizeGib || bootDiskStorageTier) {
    fields.bootDisk = {
      ...(bootDiskSizeGib ? { sizeGib: bootDiskSizeGib } : {}),
      ...(bootDiskStorageTier ? { storageTier: bootDiskStorageTier } : {}),
    };
  }

  const additionalDisks = buildFieldPolicy<FieldPolicyInit<'additionalDisks'>>(
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
  metadata: { description: values.metadata.description.trim() },
  fields: buildComputeCatalogItemFieldsPayload(values),
});
