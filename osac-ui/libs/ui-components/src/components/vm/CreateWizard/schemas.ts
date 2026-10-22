import type { TFunction } from 'i18next';
import * as yup from 'yup';

import { buildNetworkAttachmentSchemas } from '@osac/ui-components/validation/network-attachment';
import { resourceNameSchema } from '@osac/ui-components/validation/resource-name';
import { userDataSchema } from '@osac/ui-components/validation/user-data';

export type VmWizardStepId =
  | 'catalog'
  | 'general'
  | 'configuration'
  | 'storage'
  | 'networking'
  | 'review';

const lockedField = (valueSchema: yup.AnySchema) =>
  yup.object({
    value: valueSchema,
    locked: yup.boolean(),
  });

const storageTierSchema = (t: TFunction) =>
  yup
    .object({
      id: yup.string(),
      name: yup.string(),
    })
    .test('storage-tier-id-or-name', t('Storage tier is required'), (value) => {
      const id = value?.id?.trim() ?? '';
      const name = value?.name?.trim() ?? '';
      return Boolean(id || name);
    });

const buildComputeInstanceFieldDefinitions = (t: TFunction) => {
  return {
    catalogItemReference: yup.object({
      name: yup.string().required(t('catalogProvision.validation.catalogItemRequired')),
      id: yup.string().required(t('catalogProvision.validation.catalogItemRequired')),
    }),
    metadataName: resourceNameSchema(t),
    specInstanceType: lockedField(yup.string().required(t('Instance type is required'))),
    specUserData: lockedField(yup.string()).when('userDataSource', {
      is: (source: { value?: string }) => source?.value !== 'secret',
      then: () => lockedField(userDataSchema(t)),
      otherwise: () => lockedField(yup.string()),
    }),
    specBootDisk: yup.object({
      sizeGib: lockedField(
        yup
          .string()
          .test(
            'boot-disk-number',
            t('catalogProvision.validation.bootDiskNumber'),
            (value) => !value?.trim() || !Number.isNaN(Number(value)),
          )
          .required(t('catalogProvision.validation.required')),
      ),
      storageTier: lockedField(storageTierSchema(t)),
    }),
    specAdditionalDisks: lockedField(
      yup.array(
        yup.object({
          sizeGib: yup
            .string()
            .test('additional-disk-number', t('Additional disk size must be a number'), (value) => {
              const size = Number(value?.trim());
              return Number.isInteger(size) && size >= 1 && size <= 16384;
            }),
          storageTier: storageTierSchema(t),
        }),
      ),
    ),
    specNetworking: (() => {
      const na = buildNetworkAttachmentSchemas(t);
      return yup.object({
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
        autoExternalIpAttachment: lockedField(yup.boolean()),
      });
    })(),
  };
};

/**
 * Builds a Yup schema for one wizard step only.
 *
 * Formik always validates the full form values against `validationSchema`. If this
 * included every step's fields, blur and Next would fail on steps the user has not
 * reached yet (for example, empty networking while still on General). Returning
 * only the active step's fields keeps validation scoped to the current step.
 */
export const buildComputeInstanceStepSchema = (
  stepId: VmWizardStepId,
  t: TFunction,
): yup.AnyObjectSchema | undefined => {
  // Review has no editable fields; Create provisions without Formik validation.
  if (stepId === 'review') {
    return undefined;
  }

  const fields = buildComputeInstanceFieldDefinitions(t);

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
          sshKey: lockedField(yup.string()),
          diskImage: lockedField(
            yup.object({
              id: yup.string().required(t('Disk image is required')),
            }),
          ),
        }),
      });
    case 'configuration':
      return yup.object({
        spec: yup.object({
          instanceType: fields.specInstanceType,
          userDataSource: lockedField(yup.string()),
          userData: fields.specUserData,
          userDataSecret: lockedField(yup.string()),
        }),
      });
    case 'storage':
      return yup.object({
        spec: yup.object({
          bootDisk: fields.specBootDisk,
          additionalDisks: fields.specAdditionalDisks,
        }),
      });
    case 'networking':
      return yup.object({
        spec: yup.object({
          networking: fields.specNetworking,
        }),
      });
    default:
      return undefined;
  }
};
