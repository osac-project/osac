import type {
  ClusterCatalogItem,
  ClusterCatalogNodeSet,
  ClusterNodeSetMapPolicy,
} from '@osac/types';

import { clusterNodeSetItemsFromPolicy } from '../../catalog/clusterCatalogItemResourceDisplay';
import { type ResourceSelectValue, emptyResourceSelectValue } from '../../Form/resourceSelectValue';
import {
  boolFieldFromPolicy,
  catalogAttachmentFromPolicy,
  networkAttachmentFieldsFromPolicy,
} from '../../ProvisioningWizard/fields/networkAttachmentPolicy';
import {
  type ProvisioningFieldValue,
  provisioningFieldFromPolicy,
  provisioningFieldValue,
} from '../../ProvisioningWizard/fields/provisioningFieldValue';

export interface ClusterNodeSetRow {
  rowId: string;
  name: string;
  hostType: string;
  size: string;
}

export interface ClusterWizardValues {
  catalogItemId: string;
  metadata: {
    name: string;
    project: string;
  };
  spec: {
    sshPublicKey: ProvisioningFieldValue<string>;
    pullSecretSecret: ProvisioningFieldValue<string>;
    /** Selected ClusterVersion metadata.name (e.g. "4-17-0"); wrapped into spec.version at payload build. */
    versionName: ProvisioningFieldValue<string>;
    nodeSetRows: ProvisioningFieldValue<ClusterNodeSetRow[]>;
    network: {
      podCidr: ProvisioningFieldValue<string>;
      serviceCidr: ProvisioningFieldValue<string>;
    };
    useDefaultNetwork: ProvisioningFieldValue<boolean>;
    networkAttachment: {
      virtualNetwork: ResourceSelectValue;
      subnet: ResourceSelectValue;
      securityGroups: ResourceSelectValue[];
    };
    autoExternalIpAttachment: ProvisioningFieldValue<boolean>;
  };
}

export const CLUSTER_SSH_KEY_WIRE_PATH = 'ssh_public_key';
export const CLUSTER_SSH_KEY_FORM_PATH = 'spec.sshPublicKey';
export const CLUSTER_PULL_SECRET_WIRE_PATH = 'pull_secret_secret.name';
export const CLUSTER_PULL_SECRET_FORM_PATH = 'spec.pullSecretSecret';
export const CLUSTER_VERSION_WIRE_PATH = 'version';
export const CLUSTER_POD_CIDR_WIRE_PATH = 'network.pod_cidr';
export const CLUSTER_SERVICE_CIDR_WIRE_PATH = 'network.service_cidr';

export const clusterSshKeyWirePath = CLUSTER_SSH_KEY_WIRE_PATH;

export const CLUSTER_CONFIGURATION_CATALOG_PATHS = [
  CLUSTER_VERSION_WIRE_PATH,
  'spec.version',
] as const;

export const CLUSTER_NETWORKING_CATALOG_PATHS = [
  CLUSTER_POD_CIDR_WIRE_PATH,
  'spec.network.pod_cidr',
  CLUSTER_SERVICE_CIDR_WIRE_PATH,
  'spec.network.service_cidr',
] as const;

export const createNodeSetRowId = (): string => crypto.randomUUID();

export const createEmptyNodeSetRow = (): ClusterNodeSetRow => ({
  rowId: createNodeSetRowId(),
  name: '',
  hostType: '',
  size: '',
});

const hostTypeFromCatalogNodeSet = (nodeSet: ClusterCatalogNodeSet): string =>
  nodeSet.baremetalInstanceType?.id?.trim() || nodeSet.baremetalInstanceType?.name?.trim() || '';

const nodeSetRowsFromPolicy = (
  policy: ClusterNodeSetMapPolicy | undefined,
): ClusterNodeSetRow[] => {
  const items = clusterNodeSetItemsFromPolicy(policy);
  const rows = Object.entries(items ?? {}).map(([name, nodeSet]) => ({
    rowId: createNodeSetRowId(),
    name,
    hostType: hostTypeFromCatalogNodeSet(nodeSet),
    size: nodeSet.size ? String(nodeSet.size) : '',
  }));
  return rows.length > 0 ? rows : [createEmptyNodeSetRow()];
};

export const createEmptyClusterValues = (): ClusterWizardValues => ({
  catalogItemId: '',
  metadata: { name: '', project: '' },
  spec: {
    sshPublicKey: provisioningFieldValue(''),
    pullSecretSecret: provisioningFieldValue(''),
    versionName: provisioningFieldValue(''),
    nodeSetRows: provisioningFieldValue([createEmptyNodeSetRow()]),
    network: {
      podCidr: provisioningFieldValue(''),
      serviceCidr: provisioningFieldValue(''),
    },
    useDefaultNetwork: provisioningFieldValue(true),
    networkAttachment: {
      virtualNetwork: emptyResourceSelectValue(),
      subnet: emptyResourceSelectValue(),
      securityGroups: [],
    },
    autoExternalIpAttachment: provisioningFieldValue(false),
  },
});

export const getInitialFormValues = (catalogItem?: ClusterCatalogItem): ClusterWizardValues => {
  const values = createEmptyClusterValues();
  if (!catalogItem) {
    return values;
  }
  const fields = catalogItem.fields;
  const sshPublicKey =
    fields?.sshPublicKey?.behavior.case === 'editable'
      ? (fields.sshPublicKey.behavior.value.defaultValue ?? '')
      : fields?.sshPublicKey?.behavior.case === 'locked'
        ? fields.sshPublicKey.behavior.value
        : '';
  const pullSecret =
    fields?.pullSecretSecret?.behavior.case === 'editable'
      ? (fields.pullSecretSecret.behavior.value.defaultValue?.name ?? '')
      : fields?.pullSecretSecret?.behavior.case === 'locked'
        ? fields.pullSecretSecret.behavior.value.name
        : '';
  const version =
    fields?.version?.behavior.case === 'editable'
      ? (fields.version.behavior.value.defaultValue?.name ?? '')
      : fields?.version?.behavior.case === 'locked'
        ? fields.version.behavior.value.name
        : '';
  const podCidr =
    fields?.network?.podCidr?.behavior.case === 'editable'
      ? (fields.network.podCidr.behavior.value.defaultValue ?? '')
      : fields?.network?.podCidr?.behavior.case === 'locked'
        ? fields.network.podCidr.behavior.value
        : '';
  const serviceCidr =
    fields?.network?.serviceCidr?.behavior.case === 'editable'
      ? (fields.network.serviceCidr.behavior.value.defaultValue ?? '')
      : fields?.network?.serviceCidr?.behavior.case === 'locked'
        ? fields.network.serviceCidr.behavior.value
        : '';
  return {
    ...values,
    catalogItemId: catalogItem.id,
    spec: {
      ...values.spec,
      sshPublicKey: provisioningFieldFromPolicy(sshPublicKey, fields?.sshPublicKey),
      pullSecretSecret: provisioningFieldFromPolicy(pullSecret, fields?.pullSecretSecret),
      versionName: provisioningFieldFromPolicy(version, fields?.version),
      nodeSetRows: provisioningFieldFromPolicy(
        nodeSetRowsFromPolicy(fields?.nodeSets),
        fields?.nodeSets,
      ),
      network: {
        podCidr: provisioningFieldFromPolicy(podCidr, fields?.network?.podCidr),
        serviceCidr: provisioningFieldFromPolicy(serviceCidr, fields?.network?.serviceCidr),
      },
      ...(() => {
        const networkFields = networkAttachmentFieldsFromPolicy(
          fields?.networkAttachment,
          catalogAttachmentFromPolicy(fields?.networkAttachment),
        );
        return {
          useDefaultNetwork: networkFields.useDefaultNetwork,
          networkAttachment: {
            virtualNetwork: networkFields.virtualNetwork,
            subnet: networkFields.subnet,
            securityGroups: networkFields.securityGroups,
          },
          autoExternalIpAttachment: boolFieldFromPolicy(fields?.autoExternalIpAttachment),
        };
      })(),
    },
  };
};
