import type { ClusterCatalogItem, ClusterNodeSetMapPolicy } from '@osac/types';

import {
  type ClusterNodeSetRow,
  createEmptyNodeSetRow,
} from '../../../Cluster/CreateWizard/fields';
import {
  type CatalogGeneralValues,
  catalogGeneralValues,
  catalogGeneralValuesFromItem,
} from '../CatalogGeneralStep';
import {
  type ProvisioningFieldValue,
  provisioningFieldValue,
} from '../../../ProvisioningWizard/fields/provisioningFieldValue';
import { catalogValueFromPolicy } from '../catalogItemPolicy';

export interface ClusterCatalogItemValues extends CatalogGeneralValues {
  version: ProvisioningFieldValue<string>;
  nodeSets: ProvisioningFieldValue<ClusterNodeSetRow[]>;
  podCidr: ProvisioningFieldValue<string>;
  serviceCidr: ProvisioningFieldValue<string>;
}

const readStringValue = (value: unknown): string => {
  if (typeof value === 'string') {
    return value;
  }
  return '';
};

const readVersionValue = (value: unknown): string => {
  const reference = value as { id?: string; name?: string } | undefined;
  return reference?.name?.trim() || reference?.id?.trim() || '';
};

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

  return {
    ...catalogGeneralValuesFromItem(item),
    version: catalogValueFromPolicy(fields?.version, readVersionValue, ''),
    nodeSets: {
      value: readNodeSetRows(nodeSetPolicy),
      locked: nodeSetPolicy?.behavior.case === 'locked',
    },
    podCidr: catalogValueFromPolicy(fields?.network?.podCidr, readStringValue, ''),
    serviceCidr: catalogValueFromPolicy(fields?.network?.serviceCidr, readStringValue, ''),
  };
};

export type { ClusterNodeSetRow };
