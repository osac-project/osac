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
    sshPublicKey: string;
    pullSecretSecret: {
      name: string;
    };
    /** Selected ClusterVersion metadata.name (e.g. "4-17-0"); wrapped into spec.version at payload build. */
    versionName: string;
    nodeSetRows: ClusterNodeSetRow[];
    network: {
      podCidr: string;
      serviceCidr: string;
    };
    useDefaultNetwork: boolean;
    networkAttachment: {
      virtualNetwork: string;
      subnet: string;
      securityGroups: string[];
    };
    autoExternalIpAttachment: boolean;
  };
}

export const CLUSTER_SSH_KEY_WIRE_PATH = 'ssh_public_key';
export const CLUSTER_SSH_KEY_FORM_PATH = 'spec.sshPublicKey';
export const CLUSTER_PULL_SECRET_WIRE_PATH = 'pull_secret_secret.name';
export const CLUSTER_PULL_SECRET_FORM_PATH = 'spec.pullSecretSecret.name';
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
