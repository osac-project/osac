import type { TFunction } from 'i18next';
import * as yup from 'yup';

import type { ClusterCatalogItem } from '@osac/types';
import { resourceNameSchema } from '@osac/ui-components/validation/resource-name';

import type { ClusterNodeSetRow } from './fields';
import {
  CLUSTER_POD_CIDR_WIRE_PATH,
  CLUSTER_SERVICE_CIDR_WIRE_PATH,
  CLUSTER_SSH_KEY_WIRE_PATH,
  CLUSTER_VERSION_WIRE_PATH,
} from './fields';
import {
  buildCidrSchema,
  cidrsOverlap,
  isValidCidr,
} from '../../../../../validation/cidr-validation';
import {
  getCatalogFieldOverlay,
  hasCatalogFieldDefinition,
  mergeCatalogValidation,
  readCatalogFieldDefinitions,
} from '../../catalogOverlay';
import { isValidSshPublicKey } from '../../fields/credentialValidation';
import type { WizardStepId } from '../../stepIds';

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

const buildClusterFieldDefinitions = (catalogItem: unknown, t: TFunction) => {
  const definitions = readCatalogFieldDefinitions(catalogItem);

  const sshKeyOverlay = getCatalogFieldOverlay(
    CLUSTER_SSH_KEY_WIRE_PATH,
    definitions,
    t('SSH public key'),
  );
  const versionOverlay = getCatalogFieldOverlay(
    CLUSTER_VERSION_WIRE_PATH,
    definitions,
    t('Version'),
  );
  const podCidrOverlay = getCatalogFieldOverlay(
    CLUSTER_POD_CIDR_WIRE_PATH,
    definitions,
    t('Pod CIDR'),
  );
  const serviceCidrOverlay = getCatalogFieldOverlay(
    CLUSTER_SERVICE_CIDR_WIRE_PATH,
    definitions,
    t('Service CIDR'),
  );

  const sshKeyRequired = hasCatalogFieldDefinition(CLUSTER_SSH_KEY_WIRE_PATH, definitions);
  const rowSchema = nodeSetRowSchema(t);

  return {
    catalogItemId: yup.string().required(t('Select a catalog item')),
    metadataName: resourceNameSchema(t),
    specSshPublicKey: mergeCatalogValidation(
      yup
        .string()
        .test(
          'ssh-public-key',
          t(
            'SSH public key must be in the form "[TYPE] key [[EMAIL]]". Supported types are ssh-rsa, ssh-ed25519, and ecdsa-sha2-nistp256/384/521.',
          ),
          (value) => isValidSshPublicKey(value),
        ),
      sshKeyOverlay,
      sshKeyRequired,
      t('This field is required'),
    ),
    specVersionName: mergeCatalogValidation(
      yup.string().trim(),
      versionOverlay,
      true,
      t('Version is required'),
    ),
    specNodeSetRows: yup
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
    specNetwork: yup.object({
      podCidr: mergeCatalogValidation(
        buildCidrSchema(t, 'ipv4'),
        podCidrOverlay,
        false,
        t('This field is required'),
      ),
      serviceCidr: mergeCatalogValidation(
        buildCidrSchema(t, 'ipv4').test(
          'service-cidr-no-overlap',
          t('Service CIDR must not overlap the pod CIDR.'),
          function (value) {
            const parent = this.parent as { podCidr?: string } | undefined;
            const podCidr = parent?.podCidr ?? '';
            if (!value?.trim() || !podCidr.trim()) {
              return true;
            }
            if (!isValidCidr(value, 'ipv4') || !isValidCidr(podCidr, 'ipv4')) {
              return true;
            }
            return !cidrsOverlap(podCidr, value, 'ipv4');
          },
        ),
        serviceCidrOverlay,
        false,
        t('This field is required'),
      ),
    }),
    specUseDefaultNetwork: yup.boolean(),
    specNetworkAttachment: yup.object({
      virtualNetwork: yup.string(),
      subnet: yup.string(),
      securityGroups: yup.array().of(yup.string()),
    }),
    specAutoExternalIpAttachment: yup.boolean(),
  };
};

export const buildClusterStepSchema = (
  catalogItem: ClusterCatalogItem | null,
  stepId: WizardStepId,
  t: TFunction,
): yup.AnyObjectSchema | undefined => {
  if (stepId === 'review') {
    return undefined;
  }

  const fields = buildClusterFieldDefinitions(catalogItem, t);

  switch (stepId) {
    case 'catalog':
      return yup.object({
        catalogItemId: fields.catalogItemId,
      });
    case 'general':
      return yup.object({
        metadata: yup.object({
          name: fields.metadataName,
        }),
        spec: yup.object({
          sshPublicKey: fields.specSshPublicKey,
          pullSecretSecret: yup.object({
            name: yup.string().required(t('Pull secret is required')),
          }),
        }),
      });
    case 'configuration':
      return yup.object({
        spec: yup.object({
          versionName: fields.specVersionName,
          nodeSetRows: fields.specNodeSetRows,
        }),
      });
    case 'networking':
      return yup.object({
        spec: yup.object({
          network: fields.specNetwork,
          useDefaultNetwork: fields.specUseDefaultNetwork,
          networkAttachment: fields.specNetworkAttachment,
          autoExternalIpAttachment: fields.specAutoExternalIpAttachment,
        }),
      });
    default:
      return undefined;
  }
};

export type { ClusterNodeSetRow };
