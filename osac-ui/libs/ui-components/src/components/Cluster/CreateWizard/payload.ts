import { type MessageInitShape } from '@bufbuild/protobuf';

import { ClusterSchema } from '@osac/types';

import type { ClusterWizardValues } from './fields';

export const buildClusterCreatePayload = (
  values: ClusterWizardValues,
  _catalogItem?: unknown,
): MessageInitShape<typeof ClusterSchema> => {
  const spec: MessageInitShape<typeof ClusterSchema>['spec'] = {
    catalogItem: {
      id: values.catalogItemReference.id,
    },
  };

  if (!values.spec.pullSecretSecret.locked) {
    spec.pullSecretSecret = {
      name: values.spec.pullSecretSecret.value,
    };
  }

  const sshPublicKey = values.spec.sshPublicKey.value.trim();
  if (!values.spec.sshPublicKey.locked && sshPublicKey) {
    spec.sshPublicKey = sshPublicKey;
  }

  if (!values.spec.versionName.locked && values.spec.versionName.value) {
    spec.version = { name: values.spec.versionName.value };
  }

  if (!values.spec.nodeSetRows.locked) {
    const nodeSets = Object.create(null) as Record<
      string,
      { baremetalInstanceType: { id: string }; size: number }
    >;
    for (const row of values.spec.nodeSetRows.value) {
      const nodeSetId = row.name.trim();
      const hostTypeId = row.hostType;
      const size = Number(row.size);
      if (!nodeSetId || !hostTypeId || !Number.isFinite(size) || size <= 0) {
        continue;
      }
      nodeSets[nodeSetId] = { baremetalInstanceType: { id: hostTypeId }, size };
    }
    if (Object.keys(nodeSets).length > 0) {
      spec.nodeSets = nodeSets;
    }
  }

  const podCidr = values.spec.network.podCidr.locked
    ? ''
    : values.spec.network.podCidr.value.trim();
  const serviceCidr = values.spec.network.serviceCidr.locked
    ? ''
    : values.spec.network.serviceCidr.value.trim();
  if (podCidr || serviceCidr) {
    spec.network = {
      ...(podCidr ? { podCidr } : {}),
      ...(serviceCidr ? { serviceCidr } : {}),
    };
  }

  if (
    !values.spec.networkAttachment.locked &&
    !values.spec.networkAttachment.value.useDefaultNetwork
  ) {
    const subnetId = values.spec.networkAttachment.value.subnet.id.trim();
    if (subnetId) {
      spec.networkAttachment = {
        subnet: { id: subnetId },
      };
    }
  }

  if (!values.spec.autoExternalIpAttachment.locked) {
    spec.autoExternalIpAttachment = values.spec.autoExternalIpAttachment.value;
  }

  return {
    metadata: { name: values.metadata.name.trim(), project: values.metadata.project },
    spec,
  };
};
