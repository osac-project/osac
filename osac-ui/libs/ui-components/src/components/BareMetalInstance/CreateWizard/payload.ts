import { MessageInitShape, create } from '@bufbuild/protobuf';

import {
  type BareMetalInstanceCatalogItem,
  BareMetalInstanceRunStrategy,
  BareMetalInstanceSchema,
  BareMetalNetworkAttachmentSchema,
} from '@osac/types';

import type { BareMetalInstanceWizardValues } from './fields';
import { catalogFieldPolicyLocked } from '../../catalog/catalogFieldPolicyDisplay';

export const buildBareMetalInstanceCreatePayload = (
  values: BareMetalInstanceWizardValues,
  catalogItem?: BareMetalInstanceCatalogItem,
): MessageInitShape<typeof BareMetalInstanceSchema> => {
  const sshKey = values.spec.sshKey.value.trim();
  const userData = values.spec.userData.value.trim();
  const userDataSecretName = values.spec.userDataSecret.value.trim();

  const bmi: MessageInitShape<typeof BareMetalInstanceSchema> = {
    metadata: { name: values.metadata.name.trim(), project: values.metadata.project },
    spec: {
      catalogItem: {
        id: values.catalogItemReference.id,
      },
      ...(!catalogFieldPolicyLocked(catalogItem?.fields?.runStrategy) && {
        runStrategy: BareMetalInstanceRunStrategy.ALWAYS,
      }),
      ...(!values.spec.sshKey.locked && sshKey && { sshPublicKey: sshKey }),
      ...(values.spec.userDataSource.value === 'secret'
        ? !values.spec.userDataSecret.locked &&
          userDataSecretName && { userDataSecret: { name: userDataSecretName } }
        : !values.spec.userData.locked && userData && { userData }),
      ...(!values.spec.instanceType.locked &&
        values.spec.instanceType.value && {
          instanceType: {
            name: values.spec.instanceType.value,
            shared: true,
          },
        }),
      ...(!values.spec.diskImage.locked && {
        diskImage: {
          id: values.spec.diskImage.value.id,
        },
      }),
    },
  };

  // Add networking configuration
  const networkAttachment = values.spec.networkAttachment;

  if (!networkAttachment.locked) {
    if (networkAttachment.value.useDefaultNetwork) {
      bmi.spec = {
        ...bmi.spec,
        networkAttachments: [],
      };
    } else {
      bmi.spec = {
        ...bmi.spec,
        networkAttachments: [
          create(BareMetalNetworkAttachmentSchema, {
            subnet: { id: networkAttachment.value.subnet.id },
            securityGroups: networkAttachment.value.securityGroups.map((sg) => ({ id: sg.id })),
          }),
        ],
      };
    }
  }

  if (!values.spec.autoExternalIpAttachment.locked) {
    bmi.spec = {
      ...bmi.spec,
      autoExternalIpAttachment: values.spec.autoExternalIpAttachment.value,
    };
  }

  return bmi;
};
