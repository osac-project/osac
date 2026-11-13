import type { FormikErrors } from 'formik';
import type { TFunction } from 'i18next';
import * as yup from 'yup';

import { intSchema } from '@osac/ui-components/validation/number';
import { userDataSchema } from '@osac/ui-components/validation/user-data';

import type { ComputeCatalogItemValues } from './values';
import {
  catalogGeneralStepSchema,
  catalogLockedResourceField,
  catalogRequiredResourceSelectValue,
} from '../../CatalogItem/catalogValidation';

export type ComputeCatalogWizardStepId =
  | 'details'
  | 'configuration'
  | 'storage'
  | 'access-init'
  | 'review';

const diskRowSchema = (t: TFunction) =>
  yup.object({
    sizeGib: intSchema(t, 1, 16384).required(t('Disk size is required')),
    storageTier: catalogRequiredResourceSelectValue(t('Storage tier is required')),
  });

export const buildComputeCatalogStepSchema = (
  stepId: ComputeCatalogWizardStepId,
  t: TFunction,
  role: Parameters<typeof catalogGeneralStepSchema>[1],
): yup.AnyObjectSchema | undefined => {
  switch (stepId) {
    case 'details':
      return catalogGeneralStepSchema(t, role);
    case 'configuration':
      return yup.object({
        diskImage: catalogLockedResourceField(t('Disk image is required when locked')),
        instanceType: catalogLockedResourceField(t('Instance type is required when locked')),
      });
    case 'storage':
      return yup.object({
        bootDiskSizeGib: yup.object({
          locked: yup.boolean(),
          value: intSchema(t, 1, 16384).when('locked', {
            is: true,
            then: (schema) => schema.required(t('Boot disk size is required when locked')),
          }),
        }),
        bootDiskStorageTier: catalogLockedResourceField(t('Storage tier is required when locked')),
        additionalDisks: yup.object({
          locked: yup.boolean(),
          value: yup.array(diskRowSchema(t)).when('locked', {
            is: true,
            then: (schema) =>
              schema.min(1, t('At least one disk is required when this field is locked')),
          }),
        }),
      });
    case 'access-init':
      return yup
        .object({
          userData: yup.lazy((_value, options) => {
            const root = options.parent as ComputeCatalogItemValues | undefined;
            return yup.object({
              locked: yup.boolean(),
              value: userDataSchema(t, root?.guestOsFamily),
            });
          }),
          userDataSecret: yup.object({ value: yup.string() }),
        })
        .test(
          'user-data-required-when-locked',
          t('User data is required when locked'),
          function userDataRequiredWhenLocked(values) {
            if (!values?.userData?.locked) {
              return true;
            }
            const inlineValue = values.userData?.value;
            const secretValue = values.userDataSecret?.value;
            if (
              (typeof inlineValue === 'string' && inlineValue.trim()) ||
              (typeof secretValue === 'string' && secretValue)
            ) {
              return true;
            }
            return this.createError({
              path: 'userData.value',
              message: t('User data is required when locked'),
            });
          },
        );
    case 'review':
      return undefined;
    default:
      return undefined;
  }
};

export const computeCatalogStepHasErrors = (
  stepId: string,
  errors: FormikErrors<ComputeCatalogItemValues>,
): boolean => {
  switch (stepId) {
    case 'details':
      return Boolean(errors.scope || errors.metadata || errors.template);
    case 'configuration':
      return Boolean(errors.diskImage || errors.instanceType);
    case 'storage':
      return Boolean(
        errors.bootDiskSizeGib || errors.bootDiskStorageTier || errors.additionalDisks,
      );
    case 'access-init':
      return Boolean(errors.userData || errors.userDataSecret);
    default:
      return false;
  }
};
