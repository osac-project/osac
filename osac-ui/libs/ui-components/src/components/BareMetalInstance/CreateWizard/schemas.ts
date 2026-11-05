import type { TFunction } from 'i18next';
import * as yup from 'yup';

import { buildNetworkAttachmentSchemas } from '@osac/ui-components/validation/network-attachment';
import { resourceNameSchema } from '@osac/ui-components/validation/resource-name';
import { userDataSchema } from '@osac/ui-components/validation/user-data';

import type { BareMetalInstanceWizardValues } from './fields';

export type BareMetalWizardStepId =
  | 'catalog'
  | 'general'
  | 'configuration'
  | 'networking'
  | 'access'
  | 'review';

const lockedField = (valueSchema: yup.AnySchema) =>
  yup.object({
    value: valueSchema,
    locked: yup.boolean(),
  });

const hasAuthentication = (spec: BareMetalInstanceWizardValues['spec'] | undefined): boolean => {
  const userData = spec?.userData?.value;
  const selectedUserData = userData?.source === 'secret' ? userData.secret : userData?.inline;
  return Boolean(spec?.sshKey?.value || selectedUserData);
};

const buildBareMetalInstanceFieldDefinitions = (t: TFunction) => {
  return {
    catalogItemReference: yup.object({
      name: yup.string().required(t('catalogProvision.validation.catalogItemRequired')),
      id: yup.string().required(t('catalogProvision.validation.catalogItemRequired')),
    }),
    metadataName: resourceNameSchema(t),
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
      });
    case 'configuration':
      return yup.object({
        spec: yup.object({
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
    case 'access': {
      const authenticationMessage = t('Provide either an SSH public key or user data.');
      return yup.object({
        spec: yup.lazy((spec: BareMetalInstanceWizardValues['spec'] | undefined) => {
          const authenticationValid = hasAuthentication(spec);
          return yup.object({
            sshKey: lockedField(
              yup
                .string()
                .test('authentication-method', authenticationMessage, () => authenticationValid),
            ),
            userData: lockedField(
              yup.object({
                source: yup
                  .string()
                  .oneOf(['inline', 'secret'])
                  .required()
                  .test('authentication-method', authenticationMessage, () => authenticationValid),
                inline: yup.string().when('source', {
                  is: 'inline',
                  then: () => userDataSchema(t),
                }),
                secret: yup.string(),
              }),
            ),
          });
        }),
      });
    }
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
