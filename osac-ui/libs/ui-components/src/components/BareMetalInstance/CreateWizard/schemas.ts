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
    specUserData: lockedField(
      yup.object({
        source: yup.string().oneOf(['inline', 'secret']).required(),
        inline: yup.string().when('source', {
          is: 'inline',
          then: () => userDataSchema(t),
        }),
        secret: yup.string(),
      }),
    ),
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
  const fields = buildBareMetalInstanceFieldDefinitions(t);

  if (stepId === 'review') {
    return yup
      .object({
        spec: yup.object({
          sshKey: lockedField(yup.string()),
          userData: fields.specUserData,
        }),
      })
      .test(
        'authentication-method',
        t('Provide either an SSH public key or user data containing access credentials.'),
        function (values) {
          const spec = values?.spec as
            | {
                sshKey?: { value?: string };
                userData?: {
                  value?: { source?: string; inline?: string; secret?: string };
                };
              }
            | undefined;
          const userData = spec?.userData?.value;
          const isSecretSource = userData?.source === 'secret';
          if (
            hasBareMetalAuthentication(
              spec?.sshKey?.value,
              isSecretSource ? undefined : userData?.inline,
              isSecretSource ? userData?.secret : undefined,
            )
          ) {
            return true;
          }
          return this.createError({ path: 'spec.sshKey.value' });
        },
      );
  }

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
          userData: fields.specUserData,
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
