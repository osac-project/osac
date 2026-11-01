import type { MessageInitShape } from '@bufbuild/protobuf';

import { type BareMetalInstanceCatalogItem, BareMetalInstanceCatalogItemSchema } from '@osac/types';

import type { BareMetalCatalogItemValues } from './values';
import { buildUpdateMaskPaths } from '../../../../api/v1/update-mask';
import { type ResourceSelectValue } from '../../../Form/resourceSelectValue';
import { catalogGeneralItemPayload } from '../CatalogGeneralStep';
import { catalogFieldPolicyClearPath, catalogFieldPolicyFromValue } from '../catalogItemPolicy';
import { catalogUserDataPolicyFromValues } from '../catalogUserData';

type CatalogItemFieldsInit = NonNullable<
  MessageInitShape<typeof BareMetalInstanceCatalogItemSchema>['fields']
>;
type FieldPolicyInit<K extends keyof CatalogItemFieldsInit> = NonNullable<CatalogItemFieldsInit[K]>;

const isEmptyString = (value: unknown) => typeof value !== 'string' || !value.trim();
const isEmptyResource = (value: unknown) => !(value as ResourceSelectValue | undefined)?.id?.trim();

const toTrimmedString = (value: unknown) => (value as string).trim();
const toInstanceTypeReference = (value: unknown) => ({
  name: toTrimmedString(value),
  shared: true,
});
const toDiskImageReference = (value: unknown) => ({
  id: (value as ResourceSelectValue).id.trim(),
});

const buildBareMetalCatalogItemFieldsPayload = (
  values: BareMetalCatalogItemValues,
): CatalogItemFieldsInit => {
  const fields: CatalogItemFieldsInit = {};

  const instanceType = catalogFieldPolicyFromValue<FieldPolicyInit<'instanceType'>>(
    values.instanceType,
    isEmptyString,
    toInstanceTypeReference,
  );
  if (instanceType) {
    fields.instanceType = instanceType;
  }

  const diskImage = catalogFieldPolicyFromValue<FieldPolicyInit<'diskImage'>>(
    values.diskImage,
    isEmptyResource,
    toDiskImageReference,
  );
  if (diskImage) {
    fields.diskImage = diskImage;
  }

  const userDataSource = catalogUserDataPolicyFromValues(values);
  if (userDataSource) {
    fields.userDataSource = userDataSource;
  }

  return fields;
};

export const buildBareMetalCatalogItemCreatePayload = (
  values: BareMetalCatalogItemValues,
): MessageInitShape<typeof BareMetalInstanceCatalogItemSchema> => ({
  metadata: catalogGeneralItemPayload(values),
  template: { id: values.template.id.trim() },
  published: false,
  fields: buildBareMetalCatalogItemFieldsPayload(values),
});

export const buildBareMetalCatalogItemUpdatePayload = (
  _item: BareMetalInstanceCatalogItem,
  values: BareMetalCatalogItemValues,
): MessageInitShape<typeof BareMetalInstanceCatalogItemSchema> => ({
  fields: buildBareMetalCatalogItemFieldsPayload(values),
});

export const buildBareMetalCatalogItemUpdateRequest = (
  item: BareMetalInstanceCatalogItem,
  values: BareMetalCatalogItemValues,
) => {
  const payload = buildBareMetalCatalogItemUpdatePayload(item, values);
  const object = { id: item.id, ...payload };
  const fields = payload.fields;
  const clearPaths = [
    catalogFieldPolicyClearPath(
      item.fields?.instanceType,
      fields?.instanceType,
      'fields.instance_type',
    ),
    catalogFieldPolicyClearPath(item.fields?.diskImage, fields?.diskImage, 'fields.disk_image'),
    catalogFieldPolicyClearPath(
      item.fields?.userDataSource,
      fields?.userDataSource,
      'fields.user_data_source',
    ),
  ].flat();

  return {
    object,
    updateMask: {
      paths: [
        ...new Set([
          ...buildUpdateMaskPaths(object, { schema: BareMetalInstanceCatalogItemSchema }),
          ...clearPaths,
        ]),
      ],
    },
  };
};
