import type { TFunction } from 'i18next';
import * as yup from 'yup';

/** A cloud provider admin must choose the tenant a workload is provisioned in. */
export const workloadTenantSchema = (t: TFunction) =>
  yup.object({
    metadata: yup.object({
      tenant: yup.object({
        id: yup.string().required(t('Tenant is required')),
        name: yup.string(),
      }),
    }),
  });
