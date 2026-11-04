import type { MessageInitShape } from '@bufbuild/protobuf';

import { type BareMetalInstanceCatalogItem, BareMetalInstanceCatalogItemSchema } from '@osac/types';

import type { BareMetalCatalogItemValues } from './values';
import { catalogGeneralItemPayload } from '../../CatalogItem/CatalogGeneralStep';
import { catalogFieldPolicyFromValue } from '../../CatalogItem/catalogItemPolicy';
import { catalogUserDataPolicyFromValues } from '../../CatalogItem/catalogUserData';
import { type ResourceSelectValue } from '../../Form/resourceSelectValue';

type CatalogItemFieldsInit = NonNullable<
  MessageInitShape<typeof BareMetalInstanceCatalogItemSchema>['fields']
>;

const isEmptyResource = (value: ResourceSelectValue) => !value.id;

const toInstanceTypeReference = (value: ResourceSelectValue) => ({
  id: value.id,
  name: value.name,
  shared: true,
});
const buildBareMetalCatalogItemFieldsPayload = (
  values: BareMetalCatalogItemValues,
  item?: BareMetalInstanceCatalogItem,
): CatalogItemFieldsInit => {
  const fields: CatalogItemFieldsInit = {};

  const instanceType = catalogFieldPolicyFromValue(
    values.instanceType,
    isEmptyResource,
    toInstanceTypeReference,
  );
  if (instanceType) {
    fields.instanceType = instanceType;
  } else if (item?.fields?.instanceType) {
    fields.instanceType = undefined;
  }

  const diskImage = catalogFieldPolicyFromValue(values.diskImage, isEmptyResource);
  if (diskImage) {
    fields.diskImage = diskImage;
  } else if (item?.fields?.diskImage) {
    fields.diskImage = undefined;
  }

  const userDataSource = catalogUserDataPolicyFromValues(values);
  if (userDataSource) {
    fields.userDataSource = userDataSource;
  } else if (item?.fields?.userDataSource) {
    fields.userDataSource = undefined;
  }

  return fields;
};

export const buildBareMetalCatalogItemCreatePayload = (
  values: BareMetalCatalogItemValues,
): MessageInitShape<typeof BareMetalInstanceCatalogItemSchema> => ({
  metadata: catalogGeneralItemPayload(values),
  description: values.description,
  template: values.template,
  published: false,
  fields: buildBareMetalCatalogItemFieldsPayload(values),
});

export const buildBareMetalCatalogItemUpdatePayload = (
  item: BareMetalInstanceCatalogItem,
  values: BareMetalCatalogItemValues,
): MessageInitShape<typeof BareMetalInstanceCatalogItemSchema> => ({
  description: values.description,
  fields: buildBareMetalCatalogItemFieldsPayload(values, item),
});

export const buildBareMetalCatalogItemUpdateRequest = (
  item: BareMetalInstanceCatalogItem,
  values: BareMetalCatalogItemValues,
) => {
  const payload = buildBareMetalCatalogItemUpdatePayload(item, values);
  return { object: { id: item.id, ...payload } };
};
