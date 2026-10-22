import type { TFunction } from 'i18next';
import * as yup from 'yup';

import { buildNetworkAttachmentSchemas } from '@osac/ui-components/validation/network-attachment';
import { resourceNameSchema } from '@osac/ui-components/validation/resource-name';
import { userDataSchema } from '@osac/ui-components/validation/user-data';

import { hasBareMetalAuthentication } from './fields';
import { isValidSshPublicKey } from '../../ProvisioningWizard/credentialValidation';

export type BareMetalWizardStepId =
  | 'catalog'
  | 'general'
  | 'configuration'
  | 'networking'
  | 'review';

const lockedField = (valueSchema: yup.AnySchema) =>
  yup.object({
    value: valueSchema,
    locked: yup.boolean(),
  });

const buildBareMetalInstanceFieldDefinitions = (t: TFunction) => {
  return {
    catalogItemReference: yup.object({
      name: yup.string().required(t('catalogProvision.validation.catalogItemRequired')),
      id: yup.string().required(t('catalogProvision.validation.catalogItemRequired')),
    }),
    metadataName: resourceNameSchema(t),
    specSshKey: lockedField(
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
    specUserData: lockedField(yup.string()).when('userDataSource', {
      is: (source: { value?: string }) => source?.value !== 'secret',
      then: () => lockedField(userDataSchema(t)),
      otherwise: () => lockedField(yup.string()),
    }),
  };
};

/**
 * Builds a Yup schema for one wizard step only.
 *
 * Formik always validates the full form values against `validationSchema`. If this
 * included every step's fields, blur and Next would fail on steps the user has not
 * reached yet. Returning only the active step's fields keeps validation scoped to
 * the current step.
 */
export const buildBareMetalInstanceStepSchema = (
  stepId: BareMetalWizardStepId,
  t: TFunction,
): yup.AnyObjectSchema | undefined => {
  if (stepId === 'review') {
    return yup
      .object({
        spec: yup.object({
          sshKey: lockedField(yup.string()),
          userDataSource: lockedField(yup.string()),
          userData: lockedField(yup.string()),
          userDataSecret: lockedField(yup.string()),
        }),
      })
      .test(
        'authentication-method',
        t('Provide either an SSH public key or user data containing access credentials.'),
        function (values) {
          const spec = values?.spec as
            | {
                sshKey?: { value?: string };
                userDataSource?: { value?: string };
                userData?: { value?: string };
                userDataSecret?: { value?: string };
              }
            | undefined;
          const isSecretSource = spec?.userDataSource?.value === 'secret';
          if (
            hasBareMetalAuthentication(
              spec?.sshKey?.value,
              isSecretSource ? undefined : spec?.userData?.value,
              isSecretSource ? spec?.userDataSecret?.value : undefined,
            )
          ) {
            return true;
          }
          return this.createError({ path: 'spec.sshKey.value' });
        },
      );
  }

  const fields = buildBareMetalInstanceFieldDefinitions(t);

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
          sshKey: fields.specSshKey,
        }),
      });
    case 'configuration':
      return yup.object({
        spec: yup.object({
          userDataSource: lockedField(yup.string()),
          userData: fields.specUserData,
          userDataSecret: lockedField(yup.string()),
          instanceType: lockedField(yup.string()),
          diskImage: lockedField(
            yup.object({
              id: yup.string().required(t('Disk image is required')),
            }),
          ),
        }),
      });
    case 'networking':
      return buildNetworkingSchema(t);
    default:
      return undefined;
  }
};

const buildNetworkingSchema = (t: TFunction) => {
  const na = buildNetworkAttachmentSchemas(t);
  return yup.object({
    spec: yup.object({
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
      autoExternalIpAttachment: lockedField(yup.boolean().required()),
    }),
  });
};
