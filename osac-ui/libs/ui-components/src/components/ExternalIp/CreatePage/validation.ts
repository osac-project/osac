import type { FormikErrors } from 'formik';
import type { TFunction } from 'i18next';
import * as Yup from 'yup';

import { resourceNameSchema } from '@osac/ui-components/validation/resource-name';

import type { ExternalIpFormValues } from './values';

export const getExternalIpSchema = (t: TFunction, isAdmin = false) =>
  Yup.object({
    metadata: Yup.object({
      name: resourceNameSchema(t),
      ...(isAdmin && {
        tenant: Yup.object({ id: Yup.string().required(t('A tenant is required')) }),
      }),
    }),
    pool: Yup.object({
      id: Yup.string().required(t('An external IP pool is required')),
    }),
  });

export const externalIpStepHasErrors = (stepId: string, errors: FormikErrors<unknown>): boolean => {
  const formErrors = errors as FormikErrors<ExternalIpFormValues>;
  switch (stepId) {
    case 'general':
      return Boolean(
        formErrors.metadata?.name || formErrors.metadata?.project || formErrors.metadata?.tenant,
      );
    case 'configuration':
      return Boolean(formErrors.pool);
    default:
      return false;
  }
};
