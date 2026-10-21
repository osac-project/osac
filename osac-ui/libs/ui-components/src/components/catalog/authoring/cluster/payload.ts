import type { MessageInitShape } from '@bufbuild/protobuf';

import { type ClusterCatalogItem, ClusterCatalogItemSchema } from '@osac/types';

import type { ClusterCatalogItemValues } from './values';
import type { ClusterNodeSetRow } from '../../../Cluster/CreateWizard/fields';
import { catalogGeneralItemPayload } from '../CatalogGeneralStep';
import { policyFromCatalogValue } from '../catalogItemPolicy';
import type { ProvisioningFieldValue } from '../../../ProvisioningWizard/fields/provisioningFieldValue';

type CatalogItemFieldsInit = NonNullable<
  MessageInitShape<typeof ClusterCatalogItemSchema>['fields']
>;
type NetworkFieldsInit = NonNullable<CatalogItemFieldsInit['network']>;
type FieldPolicyInit<K extends keyof CatalogItemFieldsInit> = NonNullable<CatalogItemFieldsInit[K]>;

const isEmptyString = (value: unknown) => typeof value !== 'string' || !value.trim();
const isEmptyNodeSetList = (value: unknown) => !(value as ClusterNodeSetRow[] | undefined)?.length;

const toTrimmedString = (value: unknown) => (value as string).trim();
const toVersionReference = (value: unknown) => ({ name: toTrimmedString(value) });

const toNodeSetMap = (value: unknown) => {
  const items = Object.create(null) as Record<
    string,
    { size: number; baremetalInstanceType: { id: string } }
  >;

  for (const row of value as ClusterNodeSetRow[]) {
    const name = row.name.trim();
    const hostType = row.hostType.trim();
    const size = Number(row.size.trim());
    if (!name || !hostType || !Number.isFinite(size) || size <= 0) {
      continue;
    }
    items[name] = { size, baremetalInstanceType: { id: hostType } };
  }

  return { items };
};

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

const buildClusterCatalogItemFieldsPayload = (
  values: ClusterCatalogItemValues,
): CatalogItemFieldsInit => {
  const fields: CatalogItemFieldsInit = {};

  const version = buildFieldPolicy<FieldPolicyInit<'version'>>(
    values.version,
    isEmptyString,
    toVersionReference,
  );
  if (version) {
    fields.version = version;
  }

  const nodeSets = buildFieldPolicy<FieldPolicyInit<'nodeSets'>>(
    values.nodeSets,
    isEmptyNodeSetList,
    toNodeSetMap,
  );
  if (nodeSets) {
    fields.nodeSets = nodeSets;
  }

  const podCidr = buildFieldPolicy<NonNullable<NetworkFieldsInit['podCidr']>>(
    values.podCidr,
    isEmptyString,
    toTrimmedString,
  );
  const serviceCidr = buildFieldPolicy<NonNullable<NetworkFieldsInit['serviceCidr']>>(
    values.serviceCidr,
    isEmptyString,
    toTrimmedString,
  );
  if (podCidr || serviceCidr) {
    fields.network = {
      ...(podCidr ? { podCidr } : {}),
      ...(serviceCidr ? { serviceCidr } : {}),
    };
  }

  return fields;
};

export const buildClusterCatalogItemCreatePayload = (
  values: ClusterCatalogItemValues,
): MessageInitShape<typeof ClusterCatalogItemSchema> => ({
  metadata: catalogGeneralItemPayload(values),
  template: { id: values.template.id.trim() },
  published: false,
  fields: buildClusterCatalogItemFieldsPayload(values),
});

export const buildClusterCatalogItemUpdatePayload = (
  _item: ClusterCatalogItem,
  values: ClusterCatalogItemValues,
): MessageInitShape<typeof ClusterCatalogItemSchema> => ({
  metadata: { description: values.metadata.description.trim() },
  fields: buildClusterCatalogItemFieldsPayload(values),
});
