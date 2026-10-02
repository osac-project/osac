import { MessageInitShape, create } from '@bufbuild/protobuf';

import {
  BareMetalInstanceRunStrategy,
  BareMetalInstanceSchema,
  BareMetalNetworkAttachmentSchema,
} from '@osac/types';

import type { BareMetalInstanceWizardValues } from './fields';

export const buildBareMetalInstanceCreatePayload = (
  values: BareMetalInstanceWizardValues,
): MessageInitShape<typeof BareMetalInstanceSchema> => {
  const sshKey = values.spec.sshKey.trim();
  const userData = values.spec.userData.trim();
  const userDataSecretName = values.spec.userDataSecret.name.trim();

  const bmi: MessageInitShape<typeof BareMetalInstanceSchema> = {
    metadata: { name: values.metadata.name.trim(), project: values.metadata.project },
    spec: {
      catalogItem: {
        id: values.catalogItemId,
      },
      runStrategy: BareMetalInstanceRunStrategy.ALWAYS,
      ...(sshKey && { sshPublicKey: sshKey }),
      ...(values.spec.userDataSource === 'secret'
        ? userDataSecretName && { userDataSecret: { name: userDataSecretName } }
        : userData && { userData }),
      ...(values.spec.instanceType.name && {
        instanceType: {
          name: values.spec.instanceType.name,
        },
      }),
      diskImage: {
        id: values.spec.diskImage.id,
      },
    },
  };

  // Add networking configuration
  const networking = values.spec.networking;

  // Only include networkAttachments if custom networking is enabled
  if (!networking.useDefaults && networking.attachments.length > 0) {
    bmi.spec = {
      ...bmi.spec,
      networkAttachments: networking.attachments.slice(0, 1).map((attachment) =>
        create(BareMetalNetworkAttachmentSchema, {
          subnet: { id: attachment.subnet },
          securityGroups: attachment.securityGroups.map((id) => ({ id })),
        }),
      ),
    };
  }

  // Add auto external IP attachment if enabled
  if (networking.attachExternalIp) {
    bmi.spec = {
      ...bmi.spec,
      autoExternalIpAttachment: true,
    };
  }

  return bmi;
};
