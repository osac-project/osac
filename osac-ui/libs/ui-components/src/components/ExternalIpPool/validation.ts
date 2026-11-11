import type { FormikErrors } from 'formik';
import type { TFunction } from 'i18next';
import { Address4 } from 'ip-address';
import * as Yup from 'yup';

import { buildCidrSchema, isValidCidr } from '@osac/ui-components/validation/cidr-validation';
import { resourceNameSchema } from '@osac/ui-components/validation/resource-name';

import type { ExternalIpPoolFormValues } from './values';

export const getExternalIpPoolSchema = (t: TFunction) =>
  Yup.object({
    metadata: Yup.object({
      name: resourceNameSchema(t),
    }),
    cidrs: Yup.array()
      .of(
        buildCidrSchema(t, 'ipv4')
          .test(
            'canonical-ipv4-cidr',
            t('CIDR must use the network address (for example, 2.3.4.0/23)'),
            (value) => {
              if (!value || !isValidCidr(value, 'ipv4')) {
                return true;
              }

              const address = new Address4(value);
              return address.bigInt() === address.startAddress().bigInt();
            },
          )
          .required(t('CIDR is required')),
      )
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
