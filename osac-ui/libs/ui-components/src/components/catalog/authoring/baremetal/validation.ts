import type { FormikErrors } from 'formik';
import type { TFunction } from 'i18next';
import * as yup from 'yup';

import { userDataSchema } from '@osac/ui-components/validation/user-data';

import {
  catalogGeneralStepSchema,
  catalogLockedResourceField,
  catalogLockedStringField,
} from '../catalogValidation';
import type { BareMetalCatalogItemValues } from './values';

export type BareMetalCatalogWizardStepId = 'general' | 'configuration' | 'review';

export const buildBareMetalCatalogStepSchema = (
  stepId: BareMetalCatalogWizardStepId,
  t: TFunction,
  role: Parameters<typeof catalogGeneralStepSchema>[1],
): yup.AnyObjectSchema | undefined => {
  switch (stepId) {
    case 'general':
      return catalogGeneralStepSchema(t, role);
    case 'configuration':
      return yup.object({
        instanceType: catalogLockedStringField(
          yup.string().trim(),
          t('Instance type is required when locked'),
        ),
        diskImage: catalogLockedResourceField(t('Disk image is required when locked')),
        userData: catalogLockedStringField(
          userDataSchema(t),
          t('User data is required when locked'),
        ),
      });
    case 'review':
      return undefined;
    default:
      return undefined;
  }
};

export const bareMetalCatalogStepHasErrors = (
  stepId: string,
  errors: FormikErrors<BareMetalCatalogItemValues>,
): boolean => {
  switch (stepId) {
    case 'general':
      return Boolean(errors.scope || errors.metadata || errors.template);
    case 'configuration':
      return Boolean(errors.instanceType || errors.diskImage || errors.userData);
    default:
      return false;
  }
};
