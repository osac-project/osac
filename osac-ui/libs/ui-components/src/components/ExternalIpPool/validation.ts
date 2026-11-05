import type { FormikErrors } from 'formik';
import type { TFunction } from 'i18next';
import * as Yup from 'yup';

import { buildCidrSchema } from '@osac/ui-components/validation/cidr-validation';
import { resourceNameSchema } from '@osac/ui-components/validation/resource-name';

import type { ExternalIpPoolFormValues } from './values';

export const getExternalIpPoolSchema = (t: TFunction) =>
  Yup.object({
    metadata: Yup.object({
      name: resourceNameSchema(t),
    }),
    cidrs: Yup.array()
      .of(buildCidrSchema(t, 'ipv4').required(t('CIDR is required')))
      .length(1, t('Exactly one CIDR is required')),
  });

export const externalIpPoolStepHasErrors = (
  stepId: string,
  errors: FormikErrors<unknown>,
): boolean => {
  const poolErrors = errors as FormikErrors<ExternalIpPoolFormValues>;
  switch (stepId) {
    case 'pool':
      return Boolean(poolErrors.metadata?.name || poolErrors.cidrs);
    default:
      return false;
  }
};
