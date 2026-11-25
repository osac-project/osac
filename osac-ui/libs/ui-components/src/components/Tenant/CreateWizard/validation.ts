import { FormikErrors } from 'formik';
import type { TFunction } from 'i18next';
import * as Yup from 'yup';

import { resourceNameSchema } from '@osac/ui-components/validation/resource-name';

import { EXTERNAL_BILLING_ID_MAX_LENGTH, TenantValues } from './values';

const DOMAIN_LABEL = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

const isIpv4Address = (value: string): boolean => {
  const parts = value.split('.');
  if (parts.length !== 4) {
    return false;
  }
  return parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255);
};

const isValidDomain = (value: string): boolean => {
  if (value.length > 253 || isIpv4Address(value)) {
    return false;
  }
  const labels = value.split('.');
  if (labels.length < 2) {
    return false;
  }
  return labels.every((label) => DOMAIN_LABEL.test(label));
};

const domainSchema = (t: TFunction) =>
  Yup.string()
    .required(t('Domain is required'))
    .test(
      'valid-domain',
      t('Must be a valid domain (e.g. example.com)'),
      (value) => !!value && isValidDomain(value),
    );

export const getTenantSchema = (t: TFunction) =>
  Yup.object({
    name: resourceNameSchema(t),
    // Existing tenants, including the installer's shared and system tenants,
    // can have no domain at all, so an empty value is valid.
    primaryDomain: Yup.string().test(
      'valid-domain',
      t('Must be a valid domain (e.g. example.com)'),
      (value) => !value || isValidDomain(value),
    ),
    additionalDomains: Yup.array().of(domainSchema(t)),
    // The API counts code points, so count them here too. String length would
    // reject valid ids that use astral characters.
    externalBillingId: Yup.string().test(
      'max-code-points',
      t('External billing ID must be {{max}} characters or fewer', {
        max: EXTERNAL_BILLING_ID_MAX_LENGTH,
      }),
      (value) => !value || [...value].length <= EXTERNAL_BILLING_ID_MAX_LENGTH,
    ),
  }).test('unique-domains', t('Domains must be unique'), function (values: Partial<TenantValues>) {
    // Keep every field's own path. Filtering out blank values first would shift
    // the indices and blame the wrong row.
    const fields = [
      { domain: values.primaryDomain ?? '', path: 'primaryDomain' },
      ...(values.additionalDomains ?? []).map((domain, index) => ({
        domain,
        path: `additionalDomains.${index}`,
      })),
    ];
    const seen = new Set<string>();
    for (const { domain, path } of fields) {
      if (!domain) {
        continue;
      }
      if (seen.has(domain)) {
        // Place the message on the field that repeats an earlier domain. An
        // array-level error becomes a string and Formik reads its first letter.
        return this.createError({ path, message: t('Domains must be unique') });
      }
      seen.add(domain);
    }
    return true;
  });

export const tenantStepHasErrors = (
  stepId: string,
  errors: FormikErrors<TenantValues>,
): boolean => {
  switch (stepId) {
    case 'general':
      return Boolean(errors.name || errors.primaryDomain || errors.additionalDomains);
    case 'billing':
      return Boolean(errors.externalBillingId);
    default:
      return false;
  }
};
