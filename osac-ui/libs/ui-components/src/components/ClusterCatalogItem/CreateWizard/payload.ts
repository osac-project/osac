import type { MessageInitShape } from '@bufbuild/protobuf';

import { type ClusterCatalogItem, ClusterCatalogItemSchema } from '@osac/types';

import type { ClusterCatalogItemValues } from './values';
import { buildUpdateMaskPaths } from '../../../api/v1/update-mask';
import { catalogGeneralItemPayload } from '../../CatalogItem/CatalogGeneralStep';
import {
  catalogFieldPolicyClearPath,
  catalogFieldPolicyFromValue,
} from '../../CatalogItem/catalogItemPolicy';
import type { ClusterNodeSetRow } from '../../Cluster/CreateWizard/fields';
import { type ResourceSelectValue } from '../../Form/resourceSelectValue';

type CatalogItemFieldsInit = NonNullable<
  MessageInitShape<typeof ClusterCatalogItemSchema>['fields']
>;

const isEmptyString = (value: string) => !value.trim();
const isEmptyResource = (value: ResourceSelectValue) => !value.id;
const isEmptyNodeSetList = (value: ClusterNodeSetRow[]) => !value.length;

const toTrimmedString = (value: string) => value.trim();
const toVersionReference = (value: ResourceSelectValue) => ({
  id: value.id,
  name: value.name,
  shared: true,
});

const toNodeSetMap = (value: ClusterNodeSetRow[]) => {
  const items = Object.create(null) as Record<
    string,
    {
      size: number;
      baremetalInstanceType: { id: string; name?: string; shared: boolean };
    }
  >;

  for (const row of value) {
    const name = row.name.trim();
    const hostType = row.hostType;
    const size = Number(row.size.trim());
    if (!name || !hostType.id || !Number.isFinite(size) || size <= 0) {
      continue;
    }
    items[name] = {
      size,
      baremetalInstanceType: {
        id: hostType.id,
        ...(hostType.name ? { name: hostType.name } : {}),
        shared: true,
      },
    };
  }

  return { items };
};

const buildClusterCatalogItemFieldsPayload = (
  values: ClusterCatalogItemValues,
): CatalogItemFieldsInit => {
  const fields: CatalogItemFieldsInit = {};

  const version = catalogFieldPolicyFromValue(values.version, isEmptyResource, toVersionReference);
  if (version) {
    fields.version = version;
  }

  const nodeSets = catalogFieldPolicyFromValue(values.nodeSets, isEmptyNodeSetList, toNodeSetMap);
  if (nodeSets) {
    fields.nodeSets = nodeSets;
  }

  const podCidr = catalogFieldPolicyFromValue(values.podCidr, isEmptyString, toTrimmedString);
  const serviceCidr = catalogFieldPolicyFromValue(
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
  template: values.template,
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
