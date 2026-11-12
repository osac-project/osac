import type { MessageInitShape } from '@bufbuild/protobuf';

import { type ClusterCatalogItem, ClusterCatalogItemSchema } from '@osac/types';

import type { ClusterCatalogItemValues } from './values';
import { catalogGeneralItemPayload } from '../../CatalogItem/CatalogGeneralStep';
import { catalogFieldPolicyFromValue } from '../../CatalogItem/catalogItemPolicy';
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
  if (value.length > 0) {
    return {
      items: Object.fromEntries(
        value.map((row) => [
          row.name,
          {
            baremetalInstanceType: { id: row.hostType.id, name: row.hostType.name, shared: true },
            size: row.size,
          },
        ]),
      ),
    };
  }

  return { items: undefined };
};

const buildClusterCatalogItemFieldsPayload = (
  values: ClusterCatalogItemValues,
  item?: ClusterCatalogItem,
): CatalogItemFieldsInit => {
  const fields: CatalogItemFieldsInit = {};

  const version = catalogFieldPolicyFromValue(values.version, isEmptyResource, toVersionReference);
  if (version) {
    fields.version = version;
  } else if (item?.fields?.version) {
    fields.version = undefined;
  }

  const nodeSets = catalogFieldPolicyFromValue(values.nodeSets, isEmptyNodeSetList, toNodeSetMap);
  if (nodeSets) {
    fields.nodeSets = nodeSets;
  } else if (item?.fields?.nodeSets) {
    fields.nodeSets = undefined;
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
      ...(!podCidr && item?.fields?.network?.podCidr ? { podCidr: undefined } : {}),
      ...(serviceCidr ? { serviceCidr } : {}),
      ...(!serviceCidr && item?.fields?.network?.serviceCidr ? { serviceCidr: undefined } : {}),
    };
  } else if (item?.fields?.network) {
    fields.network = {
      podCidr: undefined,
      serviceCidr: undefined,
    };
  }

  return fields;
};

export const buildClusterCatalogItemCreatePayload = (
  values: ClusterCatalogItemValues,
): MessageInitShape<typeof ClusterCatalogItemSchema> => ({
  metadata: catalogGeneralItemPayload(values),
  description: values.description,
  template: values.template,
  published: false,
  fields: buildClusterCatalogItemFieldsPayload(values),
});

export const buildClusterCatalogItemUpdatePayload = (
  item: ClusterCatalogItem,
  values: ClusterCatalogItemValues,
): MessageInitShape<typeof ClusterCatalogItemSchema> => ({
  description: values.description,
  fields: buildClusterCatalogItemFieldsPayload(values, item),
});

export const buildClusterCatalogItemUpdateRequest = (
  item: ClusterCatalogItem,
  values: ClusterCatalogItemValues,
) => {
  const payload = buildClusterCatalogItemUpdatePayload(item, values);
  return { object: { id: item.id, ...payload } };
};
