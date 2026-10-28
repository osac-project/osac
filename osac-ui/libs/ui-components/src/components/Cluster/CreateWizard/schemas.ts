import type { TFunction } from 'i18next';
import * as yup from 'yup';

import { buildNetworkAttachmentSchemas } from '@osac/ui-components/validation/network-attachment';
import { resourceNameSchema } from '@osac/ui-components/validation/resource-name';

import type { ClusterNodeSetRow } from './fields';
import { buildCidrSchema, cidrsOverlap, isValidCidr } from '../../../validation/cidr-validation';
import { isValidSshPublicKey } from '../../ProvisioningWizard/credentialValidation';

export type ClusterWizardStepId = 'catalog' | 'general' | 'configuration' | 'networking' | 'review';

const lockedField = (valueSchema: yup.AnySchema) =>
  yup.object({
    value: valueSchema,
    locked: yup.boolean(),
  });

const nodeSetRowSchema = (t: TFunction) =>
  yup.object({
    rowId: yup.string().required(),
    name: resourceNameSchema(t),
    hostType: yup.string().required(t('Host type is required')),
    size: yup
      .string()
      .required(t('Pool size is required'))
      .test('pool-size-positive', t('Pool size must be greater than zero'), (value) => {
        const parsed = Number(value?.trim());
        return Number.isFinite(parsed) && parsed > 0;
      }),
  });

const buildClusterFieldDefinitions = (t: TFunction) => {
  const rowSchema = nodeSetRowSchema(t);

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
        .of(rowSchema)
        .min(1, t('At least one node set is required'))
        .test('unique-node-set-names', function (rows) {
          const seenNames = new Set<string>();
          for (const [rowIndex, row] of (rows ?? []).entries()) {
            const name = row?.name?.trim() ?? '';
            if (!name) {
              continue;
            }
            if (seenNames.has(name)) {
              return this.createError({
                path: `${this.path}[${rowIndex}].name`,
                message: t('Node set names must be unique'),
              });
            }
            seenNames.add(name);
          }
          return true;
        }),
    ),
    specNetwork: yup
      .object({
        podCidr: lockedField(buildCidrSchema(t, 'ipv4')),
        serviceCidr: lockedField(buildCidrSchema(t, 'ipv4')),
      })
      .test(
        'service-cidr-no-overlap',
        t('Service CIDR must not overlap the pod CIDR.'),
        function (network) {
          const cidrs = network as
            | { podCidr?: { value?: string }; serviceCidr?: { value?: string } }
            | undefined;
          const podCidr = cidrs?.podCidr?.value ?? '';
          const serviceCidr = cidrs?.serviceCidr?.value ?? '';
          if (!serviceCidr.trim() || !podCidr.trim()) {
            return true;
          }
          if (!isValidCidr(serviceCidr, 'ipv4') || !isValidCidr(podCidr, 'ipv4')) {
            return true;
          }
          if (!cidrsOverlap(podCidr, serviceCidr, 'ipv4')) {
            return true;
          }
          return this.createError({
            path: `${this.path}.serviceCidr.value`,
            message: t('Service CIDR must not overlap the pod CIDR.'),
          });
        },
      ),
    specAutoExternalIpAttachment: yup.boolean(),
  };
};

export const buildClusterStepSchema = (
  stepId: ClusterWizardStepId,
  t: TFunction,
): yup.AnyObjectSchema | undefined => {
  if (stepId === 'review') {
    return undefined;
  }

  const fields = buildClusterFieldDefinitions(t);

  switch (stepId) {
    case 'catalog':
      return yup.object({
        catalogItemReference: fields.catalogItemReference,
      });
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
              securityGroups: na.securityGroupsSchema.when('useDefaultNetwork', {
                is: false,
                then: (schema) => schema.min(1, t('Select at least one security group')),
              }),
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
