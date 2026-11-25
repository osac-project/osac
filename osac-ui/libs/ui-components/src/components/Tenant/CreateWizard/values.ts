import type { Tenant } from '@osac/types/private';

export const EXTERNAL_BILLING_ID_MAX_LENGTH = 256;

export interface TenantValues {
  name: string;
  primaryDomain: string;
  additionalDomains: string[];
  externalBillingId: string;
}

export const getTenantValues = (tenant?: Tenant): TenantValues => {
  const domains = tenant?.spec?.domains ?? [];

  return {
    name: tenant?.metadata?.name ?? '',
    primaryDomain: domains[0] ?? '',
    additionalDomains: domains.slice(1),
    externalBillingId: tenant?.spec?.externalBillingId ?? '',
  };
};

/** Every domain field the wizard shows, in the order the API stores them. */
export const getTenantDomains = (values: TenantValues): string[] =>
  [values.primaryDomain, ...values.additionalDomains].filter((domain) => domain.length > 0);

/** The tenant name is read-only, so only the domains can change here. */
export const areDomainsModified = (values: TenantValues, initialValues: TenantValues): boolean =>
  values.primaryDomain !== initialValues.primaryDomain ||
  values.additionalDomains.join('\n') !== initialValues.additionalDomains.join('\n');

export const isBillingStepModified = (values: TenantValues, initialValues: TenantValues): boolean =>
  values.externalBillingId !== initialValues.externalBillingId;
