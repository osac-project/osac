import type { MessageInitShape } from '@bufbuild/protobuf';

import { type BareMetalInstanceCatalogItem, BareMetalInstanceCatalogItemSchema } from '@osac/types';

import type { BareMetalCatalogItemValues } from './values';
import { type ResourceSelectValue } from '../../../Form/resourceSelectValue';
import type { ProvisioningFieldValue } from '../../../ProvisioningWizard/fields/provisioningFieldValue';
import { catalogGeneralItemPayload } from '../CatalogGeneralStep';
import { policyFromCatalogValue } from '../catalogItemPolicy';

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

const buildBareMetalCatalogItemFieldsPayload = (
  values: BareMetalCatalogItemValues,
): CatalogItemFieldsInit => {
  const fields: CatalogItemFieldsInit = {};

  const instanceType = buildFieldPolicy<FieldPolicyInit<'instanceType'>>(
    values.instanceType,
    isEmptyString,
    toInstanceTypeReference,
  );
  if (instanceType) {
    fields.instanceType = instanceType;
  }

  const diskImage = buildFieldPolicy<FieldPolicyInit<'diskImage'>>(
    values.diskImage,
    isEmptyResource,
    toDiskImageReference,
  );
  if (diskImage) {
    fields.diskImage = diskImage;
  }

  // Gap: the catalog-item backend currently accepts only inline user data;
  // UserDataField's source and secret reference are not serialized yet.
  const userData =
    values.userDataSource.value === 'inline'
      ? buildFieldPolicy<FieldPolicyInit<'userData'>>(
          values.userData,
          isEmptyString,
          toTrimmedString,
        )
      : undefined;
  if (userData) {
    fields.userData = userData;
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
  metadata: { description: values.metadata.description.trim() },
  fields: buildBareMetalCatalogItemFieldsPayload(values),
});
