import type { MessageInitShape } from '@bufbuild/protobuf';

import { type ClusterCatalogItem, ClusterCatalogItemSchema } from '@osac/types';

import type { ClusterCatalogItemValues } from './values';
import { buildUpdateMaskPaths } from '../../../../api/v1/update-mask';
import type { ClusterNodeSetRow } from '../../../Cluster/CreateWizard/fields';
import { catalogGeneralItemPayload } from '../CatalogGeneralStep';
import { catalogFieldPolicyClearPath, catalogFieldPolicyFromValue } from '../catalogItemPolicy';

type CatalogItemFieldsInit = NonNullable<
  MessageInitShape<typeof ClusterCatalogItemSchema>['fields']
>;
type NetworkFieldsInit = NonNullable<CatalogItemFieldsInit['network']>;
type FieldPolicyInit<K extends keyof CatalogItemFieldsInit> = NonNullable<CatalogItemFieldsInit[K]>;

const isEmptyString = (value: string) => !value.trim();
const isEmptyNodeSetList = (value: ClusterNodeSetRow[]) => !value.length;

const toTrimmedString = (value: string) => value.trim();
const toVersionReference = (value: string) => ({
  name: toTrimmedString(value),
  shared: true,
});

const toNodeSetMap = (value: ClusterNodeSetRow[]) => {
  const items = Object.create(null) as Record<
    string,
    { size: number; baremetalInstanceType: { id: string } }
  >;

  for (const row of value) {
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

const buildClusterCatalogItemFieldsPayload = (
  values: ClusterCatalogItemValues,
): CatalogItemFieldsInit => {
  const fields: CatalogItemFieldsInit = {};

  const version = catalogFieldPolicyFromValue<
    string,
    ReturnType<typeof toVersionReference>,
    FieldPolicyInit<'version'>
  >(values.version, isEmptyString, toVersionReference);
  if (version) {
    fields.version = version;
  }

  const nodeSets = catalogFieldPolicyFromValue<
    ClusterNodeSetRow[],
    ReturnType<typeof toNodeSetMap>,
    FieldPolicyInit<'nodeSets'>
  >(values.nodeSets, isEmptyNodeSetList, toNodeSetMap);
  if (nodeSets) {
    fields.nodeSets = nodeSets;
  }

  const podCidr = catalogFieldPolicyFromValue<
    string,
    ReturnType<typeof toTrimmedString>,
    NonNullable<NetworkFieldsInit['podCidr']>
  >(values.podCidr, isEmptyString, toTrimmedString);
  const serviceCidr = catalogFieldPolicyFromValue<
    string,
    ReturnType<typeof toTrimmedString>,
    NonNullable<NetworkFieldsInit['serviceCidr']>
  >(values.serviceCidr, isEmptyString, toTrimmedString);
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
  fields: buildClusterCatalogItemFieldsPayload(values),
});

export const buildClusterCatalogItemUpdateRequest = (
  item: ClusterCatalogItem,
  values: ClusterCatalogItemValues,
) => {
  const payload = buildClusterCatalogItemUpdatePayload(item, values);
  const object = { id: item.id, ...payload };
  const fields = payload.fields;
  const clearPaths = [
    catalogFieldPolicyClearPath(item.fields?.version, fields?.version, 'fields.version'),
    catalogFieldPolicyClearPath(item.fields?.nodeSets, fields?.nodeSets, 'fields.node_sets'),
    catalogFieldPolicyClearPath(
      item.fields?.network?.podCidr,
      fields?.network?.podCidr,
      'fields.network.pod_cidr',
    ),
    catalogFieldPolicyClearPath(
      item.fields?.network?.serviceCidr,
      fields?.network?.serviceCidr,
      'fields.network.service_cidr',
    ),
  ].flat();

  return {
    object,
    updateMask: {
      paths: [
        ...new Set([
          ...buildUpdateMaskPaths(object, { schema: ClusterCatalogItemSchema }),
          ...clearPaths,
        ]),
      ],
    },
  };
};
