import type {
  ClusterCatalogItem,
  ClusterCatalogNodeSet,
  ClusterNodeSetMapPolicy,
} from '@osac/types';

import {
  type NetworkAttachmentFormFields,
  getEmptyNetworkAttachmentFormFields,
  networkAttachmentFieldsFromPolicy,
} from '../../ProvisioningWizard/fields/networkAttachments';
import {
  type ProvisioningFieldValue,
  getFieldFromCatalogItemPolicy,
  provisioningFieldValue,
} from '../../ProvisioningWizard/fields/provisioningFieldValue';

export interface ClusterNodeSetRow {
  rowId: string;
  name: string;
  hostType: string;
  size: string;
}

export interface ClusterWizardValues {
  catalogItemReference: { name: string; id: string };
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
    networkAttachment: ProvisioningFieldValue<NetworkAttachmentFormFields>;
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

export const createEmptyClusterValues = (): ClusterWizardValues => ({
  catalogItemReference: { name: '', id: '' },
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
    networkAttachment: provisioningFieldValue(getEmptyNetworkAttachmentFormFields()),
    autoExternalIpAttachment: provisioningFieldValue(false),
  },
});

export const getInitialFormValues = (catalogItem?: ClusterCatalogItem): ClusterWizardValues => {
  const values = createEmptyClusterValues();
  if (!catalogItem) {
    return values;
  }
  const fields = catalogItem.fields;
  const networkAttachment = networkAttachmentFieldsFromPolicy(fields?.networkAttachment);
  return {
    ...values,
    catalogItemReference: {
      name: catalogItem.metadata?.name ?? '',
      id: catalogItem.id,
    },

    spec: {
      ...values.spec,
      sshPublicKey: getFieldFromCatalogItemPolicy(fields?.sshPublicKey, ''),
      pullSecretSecret: (() => {
        const field = getFieldFromCatalogItemPolicy<{ name: string }>(fields?.pullSecretSecret, {
          name: '',
        });
        return provisioningFieldValue(field.value.name ?? '', field.locked);
      })(),
      versionName: (() => {
        const field = getFieldFromCatalogItemPolicy<{ name: string }>(fields?.version, {
          name: '',
        });
        return provisioningFieldValue(field.value.name ?? '', field.locked);
      })(),
      nodeSetRows: nodeSetRowsFromPolicy(fields?.nodeSets),
      network: {
        podCidr: getFieldFromCatalogItemPolicy(fields?.network?.podCidr, ''),
        serviceCidr: getFieldFromCatalogItemPolicy(fields?.network?.serviceCidr, ''),
      },
      networkAttachment,
      autoExternalIpAttachment: getFieldFromCatalogItemPolicy(
        fields?.autoExternalIpAttachment,
        false,
      ),
    },
  };
};

const hostTypeFromCatalogNodeSet = (nodeSet: ClusterCatalogNodeSet): string =>
  nodeSet.baremetalInstanceType?.id?.trim() || nodeSet.baremetalInstanceType?.name?.trim() || '';

const nodeSetRowsFromPolicy = (
  policy: ClusterNodeSetMapPolicy | undefined,
): ProvisioningFieldValue<ClusterNodeSetRow[]> => {
  const behavior = policy?.behavior;
  const items =
    behavior?.case === 'locked'
      ? behavior.value.items
      : behavior?.case === 'editable'
        ? behavior.value.defaultValue?.items
        : undefined;
  const rows = Object.entries(items ?? {}).map(([name, nodeSet]) => ({
    rowId: createNodeSetRowId(),
    name,
    hostType: hostTypeFromCatalogNodeSet(nodeSet),
    size: nodeSet.size ? String(nodeSet.size) : '',
  }));

  return provisioningFieldValue(
    rows.length > 0 ? rows : [createEmptyNodeSetRow()],
    behavior?.case === 'locked',
  );
};
