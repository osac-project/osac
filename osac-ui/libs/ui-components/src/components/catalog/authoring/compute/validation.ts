import type { FormikErrors } from 'formik';
import type { TFunction } from 'i18next';
import * as yup from 'yup';

import { userDataSchema } from '@osac/ui-components/validation/user-data';

import {
  catalogGeneralStepSchema,
  catalogLockedResourceField,
  catalogLockedStringField,
  catalogRequiredResourceSelectValue,
} from '../catalogValidation';
import type { ComputeCatalogItemValues } from './values';

export type ComputeCatalogWizardStepId =
  | 'details'
  | 'configuration'
  | 'storage'
  | 'access-init'
  | 'review';

const diskSizeNumberTest = (t: TFunction) =>
  ({
    name: 'disk-size-number',
    message: t('Disk size must be a number'),
    test: (value?: string) => {
      if (!value?.trim()) {
        return true;
      }
      const size = Number(value.trim());
      return Number.isInteger(size) && size >= 1 && size <= 16384;
    },
  }) as const;

const diskRowSchema = (t: TFunction) =>
  yup.object({
    sizeGib: yup.string().test(diskSizeNumberTest(t)),
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
        bootDiskSizeGib: catalogLockedStringField(
          yup.string().test(diskSizeNumberTest(t)),
          t('Boot disk size is required when locked'),
        ),
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
          userData: yup.object({ locked: yup.boolean(), value: userDataSchema(t) }),
        })
        .test(
          'user-data-required-when-locked',
          t('User data is required when locked'),
          function userDataRequiredWhenLocked(values) {
            if (!values?.userData?.locked) {
              return true;
            }
            const value = values.userData?.value;
            if (typeof value === 'string' && value.trim()) {
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
      return Boolean(errors.userData);
    default:
      return false;
  }
};
