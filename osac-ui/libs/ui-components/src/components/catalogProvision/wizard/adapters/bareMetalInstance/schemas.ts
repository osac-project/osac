import type { TFunction } from 'i18next';
import * as yup from 'yup';

import { buildNetworkAttachmentSchemas } from '@osac/ui-components/validation/network-attachment';
import { resourceNameSchema } from '@osac/ui-components/validation/resource-name';
import { userDataSchema } from '@osac/ui-components/validation/user-data';

import { hasBareMetalAuthentication } from './fields';
import { isValidSshPublicKey } from '../../fields/credentialValidation';
import type { WizardStepId } from '../../stepIds';

/**
 * Builds a Yup schema for one wizard step only.
 *
 * Formik always validates the full form values against `validationSchema`. If this
 * included every step's fields, blur and Next would fail on steps the user has not
 * reached yet. Returning only the active step's fields keeps validation scoped to
 * the current step.
 */
export const buildBareMetalInstanceStepSchema = (
  stepId: WizardStepId,
  t: TFunction,
): yup.AnyObjectSchema | undefined => {
  if (stepId === 'review') {
    return yup
      .object({
        spec: yup.object({
          sshKey: yup.string(),
          userDataSource: yup.string(),
          userData: yup.string(),
          userDataSecret: yup.object({ name: yup.string() }),
        }),
      })
      .test(
        'authentication-method',
        t('Provide either an SSH public key or user data containing access credentials.'),
        function (values) {
          const isSecretSource = values?.spec?.userDataSource === 'secret';
          if (
            hasBareMetalAuthentication(
              values?.spec?.sshKey,
              isSecretSource ? undefined : values?.spec?.userData,
              isSecretSource ? values?.spec?.userDataSecret?.name : undefined,
            )
          ) {
            return true;
          }
          return this.createError({ path: 'spec.sshKey' });
        },
      );
  }

  switch (stepId) {
    case 'catalog':
      return yup.object({
        catalogItemId: yup.string().required(t('Select a catalog item')),
      });
    case 'general':
      return yup.object({
        metadata: yup.object({
          name: resourceNameSchema(t),
        }),
        spec: yup.object({
          sshKey: yup
            .string()
            .test(
              'ssh-public-key',
              t(
                'SSH public key must be in the form "[TYPE] key [[EMAIL]]". Supported types are ssh-rsa, ssh-ed25519, and ecdsa-sha2-nistp256/384/521.',
              ),
              (value) => isValidSshPublicKey(value),
            ),
        }),
      });
    case 'configuration':
      return yup.object({
        spec: yup.object({
          userData: yup.string().when('userDataSource', {
            is: 'inline',
            then: () => userDataSchema(t),
            otherwise: (schema) => schema.notRequired(),
          }),
          instanceType: yup.object({
            name: yup.string(),
          }),
          diskImage: yup.object({
            id: yup.string().required(t('Disk image is required')),
          }),
        }),
      });
    case 'networking':
      return buildNetworkingSchema(t);
    default:
      return undefined;
  }
};

const buildAttachmentRowSchema = (t: TFunction) => {
  const na = buildNetworkAttachmentSchemas(t);
  return yup.object({
    id: yup.string().required(),
    virtualNetwork: na.requiredVirtualNetwork,
    subnet: na.requiredSubnet,
    securityGroups: na.securityGroupsSchema,
  });
};

const buildCustomAttachmentsSchema = (t: TFunction) =>
  yup
    .array()
    .of(buildAttachmentRowSchema(t))
    .min(1, t('At least one attachment is required'))
    .length(1, t('Exactly one network attachment is required'));

const buildNetworkingSchema = (t: TFunction) =>
  yup.object({
    spec: yup.object({
      networking: yup.object({
        useDefaults: yup.boolean().required(),
        attachments: yup.array().when('useDefaults', {
          is: false,
          then: () => buildCustomAttachmentsSchema(t),
          otherwise: () => yup.array(),
        }),
        attachExternalIp: yup.boolean().required(),
      }),
    }),
  });
