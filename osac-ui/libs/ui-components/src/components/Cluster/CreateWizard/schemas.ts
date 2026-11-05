import type { TFunction } from 'i18next';
import * as yup from 'yup';

import { buildNetworkAttachmentSchemas } from '@osac/ui-components/validation/network-attachment';
import { resourceNameSchema } from '@osac/ui-components/validation/resource-name';
import { workloadTenantSchema } from '@osac/ui-components/validation/workload-tenant';

import type { ClusterNodeSetRow } from './fields';
import { buildCidrSchema } from '../../../validation/cidr-validation';
import { isValidSshPublicKey } from '../../ProvisioningWizard/credentialValidation';
import type { ProvisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';
import {
  clusterNetworkDoNotOverlapTest,
  clusterNodeSetRowSchema,
  uniqueNodeSetNamesTest,
} from '../validation';

export type ClusterWizardStepId = 'catalog' | 'general' | 'configuration' | 'networking' | 'review';

const lockedField = (valueSchema: yup.AnySchema) =>
  yup.object({
    value: valueSchema,
    locked: yup.boolean(),
  });

const buildClusterFieldDefinitions = (t: TFunction) => {
  return {
    catalogItemReference: yup.object({
      name: yup.string().required(t('Select a catalog item')),
      id: yup.string().required(t('Select a catalog item')),
    }),
    metadataName: resourceNameSchema(t),
    specSshPublicKey: lockedField(
      yup
        .string()
        .test(
          'ssh-public-key',
          t(
            'SSH public key must be in the form "[TYPE] key [[EMAIL]]". Supported types are ssh-rsa, ssh-ed25519, and ecdsa-sha2-nistp256/384/521.',
          ),
          (value) => isValidSshPublicKey(value),
        ),
    ),
    specVersionName: lockedField(yup.string().trim().required(t('Version is required'))),
    specNodeSetRows: lockedField(
      yup
        .array()
        .of(clusterNodeSetRowSchema(t))
        .min(1, t('At least one node set is required'))
        .test('unique-node-set-names', uniqueNodeSetNamesTest(t)),
    ),
    specNetwork: yup
      .object({
        podCidr: yup.object<ProvisioningFieldValue<string>>({
          value: buildCidrSchema(t, 'ipv4'),
          locked: yup.boolean(),
        }),
        serviceCidr: yup.object<ProvisioningFieldValue<string>>({
          value: buildCidrSchema(t, 'ipv4'),
          locked: yup.boolean(),
        }),
      })
      .test('service-cidr-no-overlap', clusterNetworkDoNotOverlapTest(t)),
    specAutoExternalIpAttachment: yup.boolean(),
  };
};

export const buildClusterStepSchema = (
  stepId: ClusterWizardStepId,
  t: TFunction,
  options: { requireTenant?: boolean } = {},
): yup.AnyObjectSchema | undefined => {
  if (stepId === 'review') {
    return undefined;
  }

  const fields = buildClusterFieldDefinitions(t);

  switch (stepId) {
    case 'catalog': {
      const catalog = yup.object({
        catalogItemReference: fields.catalogItemReference,
      });
      return options.requireTenant ? catalog.concat(workloadTenantSchema(t)) : catalog;
    }
    case 'general':
      return yup.object({
        metadata: yup.object({
          name: fields.metadataName,
        }),
        spec: yup.object({
          sshPublicKey: fields.specSshPublicKey,
          pullSecretSecret: lockedField(yup.string().required(t('Pull secret is required'))),
        }),
      });
    case 'configuration':
      return yup.object({
        spec: yup.object({
          versionName: fields.specVersionName,
          nodeSetRows: fields.specNodeSetRows,
        }),
      });
    case 'networking': {
      const na = buildNetworkAttachmentSchemas(t);
      return yup.object({
        spec: yup.object({
          network: fields.specNetwork,
          networkAttachment: lockedField(
            yup.object({
              useDefaultNetwork: yup.boolean().required(),
              virtualNetwork: na.optionalResourceSelect.when('useDefaultNetwork', {
                is: false,
                then: () => na.requiredVirtualNetwork,
              }),
              subnet: na.optionalResourceSelect.when('useDefaultNetwork', {
                is: false,
                then: () => na.requiredSubnet,
              }),
              securityGroups: na.securityGroupsSchema,
            }),
          ),
          autoExternalIpAttachment: lockedField(fields.specAutoExternalIpAttachment),
        }),
      });
    }
    default:
      return undefined;
  }
};

export type { ClusterNodeSetRow };
