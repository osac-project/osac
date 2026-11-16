import { type MessageInitShape } from '@bufbuild/protobuf';

import { ClusterSchema } from '@osac/types';

import type { ClusterWizardValues } from './fields';

export const buildClusterCreatePayload = (
  values: ClusterWizardValues,
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

  if (!values.spec.nodeSetRows.locked && !!values.spec.nodeSetRows.value.length) {
    spec.nodeSets = Object.fromEntries(
      values.spec.nodeSetRows.value.map((row) => [
        row.name,
        { baremetalInstanceType: { id: row.hostType.id }, size: row.size },
      ]),
    );
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
      const securityGroups = values.spec.networkAttachment.value.securityGroups
        .filter((group) => group.id.trim())
        .map((group) => ({ id: group.id }));
      spec.networkAttachment = {
        subnet: { id: subnetId },
        ...(securityGroups.length > 0 ? { securityGroups } : {}),
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
