import type {
  ClusterCatalogItem,
  ClusterNodeSetMapPolicy,
  ClusterVersionReference,
} from '@osac/types';

import {
  type ClusterNodeSetRow,
  createEmptyNodeSetRow,
} from '../../../Cluster/CreateWizard/fields';
import {
  type ProvisioningFieldValue,
  getFieldFromCatalogItemPolicy,
  provisioningFieldValue,
} from '../../../ProvisioningWizard/fields/provisioningFieldValue';
import {
  type CatalogGeneralValues,
  catalogGeneralValues,
  catalogGeneralValuesFromItem,
} from '../CatalogGeneralStep';
export interface ClusterCatalogItemValues extends CatalogGeneralValues {
  version: ProvisioningFieldValue<string>;
  nodeSets: ProvisioningFieldValue<ClusterNodeSetRow[]>;
  podCidr: ProvisioningFieldValue<string>;
  serviceCidr: ProvisioningFieldValue<string>;
}

const readStringValue = (value: string | undefined): string => value ?? '';

const readVersionValue = (value: ClusterVersionReference | undefined): string =>
  value?.name?.trim() || value?.id?.trim() || '';

const readNodeSetRows = (policy: ClusterNodeSetMapPolicy | undefined) => {
  const behavior = policy?.behavior;
  const items =
    behavior?.case === 'locked'
      ? behavior.value.items
      : behavior?.case === 'editable'
        ? behavior.value.defaultValue?.items
        : undefined;

  return Object.entries(items ?? {}).map(([name, nodeSet]) => ({
    ...createEmptyNodeSetRow(),
    name,
    hostType: nodeSet.baremetalInstanceType?.id || nodeSet.baremetalInstanceType?.name || '',
    size: nodeSet.size ? String(nodeSet.size) : '',
  }));
};

export const emptyClusterCatalogItemValues = (): ClusterCatalogItemValues => ({
  ...catalogGeneralValues(),
  version: provisioningFieldValue(''),
  nodeSets: provisioningFieldValue([]),
  podCidr: provisioningFieldValue(''),
  serviceCidr: provisioningFieldValue(''),
});

export const getClusterCatalogItemInitialValues = (
  item?: ClusterCatalogItem,
): ClusterCatalogItemValues => {
  if (!item) {
    return emptyClusterCatalogItemValues();
  }

  const fields = item.fields;
  const nodeSetPolicy = fields?.nodeSets;
  const version = getFieldFromCatalogItemPolicy<ClusterVersionReference | undefined>(
    fields?.version,
    undefined,
  );
  const podCidr = getFieldFromCatalogItemPolicy<string | undefined>(
    fields?.network?.podCidr,
    undefined,
  );
  const serviceCidr = getFieldFromCatalogItemPolicy<string | undefined>(
    fields?.network?.serviceCidr,
    undefined,
  );

  return {
    ...catalogGeneralValuesFromItem(item),
    version: provisioningFieldValue(readVersionValue(version.value), version.locked),
    nodeSets: {
      value: readNodeSetRows(nodeSetPolicy),
      locked: nodeSetPolicy?.behavior.case === 'locked',
    },
    podCidr: provisioningFieldValue(readStringValue(podCidr.value), podCidr.locked),
    serviceCidr: provisioningFieldValue(readStringValue(serviceCidr.value), serviceCidr.locked),
  };
};

export type { ClusterNodeSetRow };
