import { type MessageInitShape } from '@bufbuild/protobuf';

import { type ClusterCatalogItem, ClusterSchema } from '@osac/types';

import type { ClusterWizardValues } from './fields';
import { createEmptyNodeSetRow } from './fields';

export const createEmptyClusterValues = (): ClusterWizardValues => ({
  catalogItemId: '',
  metadata: { name: '', project: '' },
  spec: {
    sshPublicKey: '',
    pullSecretSecret: {
      name: '',
    },
    versionName: '',
    nodeSetRows: [createEmptyNodeSetRow()],
    network: {
      podCidr: '',
      serviceCidr: '',
    },
    useDefaultNetwork: true,
    networkAttachment: {
      virtualNetwork: '',
      subnet: '',
      securityGroups: [],
    },
    autoExternalIpAttachment: false,
  },
});

export const buildClusterCreatePayload = (
  values: ClusterWizardValues,
  catalogItem: ClusterCatalogItem,
): MessageInitShape<typeof ClusterSchema> => {
  const spec: MessageInitShape<typeof ClusterSchema>['spec'] = {
    catalogItem: {
      id: catalogItem.id,
    },
    pullSecretSecret: {
      name: values.spec.pullSecretSecret.name,
    },
  };

  const sshPublicKey = values.spec.sshPublicKey.trim();
  if (sshPublicKey) {
    spec.sshPublicKey = sshPublicKey;
  }

  if (values.spec.versionName) {
    spec.version = { name: values.spec.versionName };
  }

  const nodeSets = Object.create(null) as Record<
    string,
    { hostType: { id: string }; size: number }
  >;
  for (const row of values.spec.nodeSetRows) {
    const nodeSetId = row.name.trim();
    const hostTypeId = row.hostType;
    const size = Number(row.size);
    if (!nodeSetId || !hostTypeId || !Number.isFinite(size) || size <= 0) {
      continue;
    }
    nodeSets[nodeSetId] = { hostType: { id: hostTypeId }, size };
  }
  if (Object.keys(nodeSets).length > 0) {
    spec.nodeSets = nodeSets;
  }

  const podCidr = values.spec.network.podCidr.trim();
  const serviceCidr = values.spec.network.serviceCidr.trim();
  if (podCidr || serviceCidr) {
    spec.network = {
      ...(podCidr ? { podCidr } : {}),
      ...(serviceCidr ? { serviceCidr } : {}),
    };
  }

  if (!values.spec.useDefaultNetwork) {
    const subnetName = values.spec.networkAttachment.subnet.trim();
    if (subnetName) {
      spec.networkAttachment = {
        subnet: { name: subnetName },
        securityGroups: values.spec.networkAttachment.securityGroups
          .filter((sg) => sg.trim())
          .map((sg) => ({ name: sg })),
      };
    }
  }

  if (values.spec.autoExternalIpAttachment) {
    spec.autoExternalIpAttachment = true;
  }

  return {
    metadata: { name: values.metadata.name.trim(), project: values.metadata.project },
    spec,
  };
};
