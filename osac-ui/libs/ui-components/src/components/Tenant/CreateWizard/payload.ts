import { MessageInitShape } from '@bufbuild/protobuf';

import { TenantSchema } from '@osac/types/private';

import {
  TenantValues,
  areDomainsModified,
  getTenantDomains,
  isBillingStepModified,
} from './values';

export const buildTenantCreatePayload = (
  values: TenantValues,
): MessageInitShape<typeof TenantSchema> => ({
  metadata: {
    name: values.name,
  },
  spec: {
    domains: getTenantDomains(values),
    externalBillingId: values.externalBillingId || undefined,
  },
});

/**
 * Only the fields the user changed. `useUpdateResource` derives the update mask
 * from this object, so a billing-only save cannot overwrite a concurrent domain
 * change. An empty billing id stays in the object (with no value), which keeps
 * its mask path and clears the stored value.
 */
export const buildTenantUpdatePayload = (
  values: TenantValues,
  initialValues: TenantValues,
): MessageInitShape<typeof TenantSchema> => {
  const spec: MessageInitShape<typeof TenantSchema>['spec'] = {};

  if (areDomainsModified(values, initialValues)) {
    spec.domains = getTenantDomains(values);
  }
  if (isBillingStepModified(values, initialValues)) {
    spec.externalBillingId = values.externalBillingId || undefined;
  }

  return { spec };
};
