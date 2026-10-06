import type { FormikErrors } from 'formik';
import type { TFunction } from 'i18next';
import * as Yup from 'yup';

import { VolumeAccessMode } from '@osac/types';
import { positiveIntegerSchema } from '@osac/ui-components/validation/positive-integer';
import { resourceNameSchema } from '@osac/ui-components/validation/resource-name';

import type { VolumeFormValues } from './values';

export const getVolumeValidationSchema = (t: TFunction) =>
  Yup.object({
    metadata: Yup.object({
      name: resourceNameSchema(t),
      description: Yup.string(),
    }),
    spec: Yup.object({
      storageTier: Yup.object({
        id: Yup.string().required(t('A storage tier is required')),
      }),
      sizeGib: positiveIntegerSchema(t),
      accessMode: Yup.mixed<VolumeAccessMode>()
        .oneOf(
          [
            VolumeAccessMode.READ_WRITE_ONCE,
            VolumeAccessMode.READ_ONLY_MANY,
            VolumeAccessMode.READ_WRITE_MANY,
            VolumeAccessMode.READ_WRITE_ONCE_POD,
          ],
          t('An access mode is required'),
        )
        .required(t('An access mode is required')),
    }),
  });

export const volumeStepHasErrors = (stepId: string, errors: FormikErrors<unknown>): boolean => {
  const formErrors = errors as FormikErrors<VolumeFormValues>;

  switch (stepId) {
    case 'general':
      return Boolean(formErrors.metadata?.name);
    case 'configuration':
      return Boolean(
        formErrors.spec?.storageTier || formErrors.spec?.sizeGib || formErrors.spec?.accessMode,
      );
    default:
      return false;
  }
};
