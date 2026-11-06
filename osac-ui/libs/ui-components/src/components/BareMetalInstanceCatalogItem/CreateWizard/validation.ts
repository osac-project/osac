import type { FormikErrors } from 'formik';
import type { TFunction } from 'i18next';
import * as yup from 'yup';

import { userDataSchema } from '@osac/ui-components/validation/user-data';

import type { BareMetalCatalogItemValues } from './values';
import {
  catalogGeneralStepSchema,
  catalogLockedResourceField,
} from '../../CatalogItem/catalogValidation';

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
      return yup
        .object({
          instanceType: catalogLockedResourceField(t('Instance type is required when locked')),
          diskImage: catalogLockedResourceField(t('Disk image is required when locked')),
          userData: yup.lazy((_value, options) => {
            const root = options.parent as BareMetalCatalogItemValues | undefined;
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

export const bareMetalCatalogStepHasErrors = (
  stepId: string,
  errors: FormikErrors<BareMetalCatalogItemValues>,
): boolean => {
  switch (stepId) {
    case 'general':
      return Boolean(errors.scope || errors.metadata || errors.template);
    case 'configuration':
      return Boolean(
        errors.instanceType || errors.diskImage || errors.userData || errors.userDataSecret,
      );
    default:
      return false;
  }
};
