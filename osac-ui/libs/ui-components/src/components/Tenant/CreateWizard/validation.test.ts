import { describe, expect, it } from 'vitest';

import { getTenantSchema } from './validation';
import { EXTERNAL_BILLING_ID_MAX_LENGTH } from './values';

const t = ((key: string) => key) as unknown as Parameters<typeof getTenantSchema>[0];

const validate = (values: Record<string, unknown>) =>
  getTenantSchema(t).validateSync(
    { name: 'acme', primaryDomain: '', additionalDomains: [], externalBillingId: '', ...values },
    { abortEarly: false },
  );

const errorsOf = (values: Record<string, unknown>): Record<string, string> => {
  try {
    validate(values);
    return {};
  } catch (error) {
    const { inner } = error as { inner: Array<{ path?: string; errors: string[] }> };
    return Object.fromEntries(
      inner.map((item) => [String(item.path), item.errors[0] ?? 'invalid']),
    );
  }
};

describe('getTenantSchema', () => {
  it('accepts a tenant with no email domain', () => {
    expect(() => validate({})).not.toThrow();
  });

  it('rejects a primary domain that is not a DNS name', () => {
    expect(errorsOf({ primaryDomain: 'not-a-domain' })).toHaveProperty('primaryDomain');
  });

  it('rejects an IPv4 address as a domain', () => {
    expect(errorsOf({ primaryDomain: '10.0.0.1' })).toHaveProperty('primaryDomain');
  });

  it('accepts a valid primary domain', () => {
    expect(errorsOf({ primaryDomain: 'acme.example.com' })).not.toHaveProperty('primaryDomain');
  });

  it('requires every additional domain row to be filled', () => {
    expect(errorsOf({ additionalDomains: [''] })).toHaveProperty('additionalDomains[0]');
  });

  it('reports a duplicate on the field that repeats an earlier domain', () => {
    const errors = errorsOf({
      primaryDomain: 'acme.example.com',
      additionalDomains: ['acme.example.com'],
    });
    expect(errors['additionalDomains.0']).toBe('Domains must be unique');
  });

  it('reports a duplicate between two additional domains on the later field', () => {
    const errors = errorsOf({
      primaryDomain: 'acme.example.com',
      additionalDomains: ['eu.example.com', 'eu.example.com'],
    });
    expect(errors['additionalDomains.1']).toBe('Domains must be unique');
  });

  it('keeps field indices when the primary domain is blank', () => {
    const errors = errorsOf({
      primaryDomain: '',
      additionalDomains: ['acme.example.com', 'acme.example.com'],
    });
    expect(errors['additionalDomains.1']).toBe('Domains must be unique');
    expect(errors['additionalDomains.0']).toBeUndefined();
  });

  it('accepts an external billing id at the length limit', () => {
    const value = 'A'.repeat(EXTERNAL_BILLING_ID_MAX_LENGTH);
    expect(errorsOf({ externalBillingId: value })).not.toHaveProperty('externalBillingId');
  });

  it('rejects an external billing id over the length limit', () => {
    const value = 'A'.repeat(EXTERNAL_BILLING_ID_MAX_LENGTH + 1);
    expect(errorsOf({ externalBillingId: value })).toHaveProperty('externalBillingId');
  });

  it('counts code points, so supplementary characters within the limit pass', () => {
    // 129 astral characters are 258 UTF-16 units but 129 code points, which the
    // API accepts.
    const value = String.fromCodePoint(0x1f600).repeat(129);
    expect(errorsOf({ externalBillingId: value })).not.toHaveProperty('externalBillingId');
  });

  it('accepts an external billing id at the limit in code points', () => {
    const value = String.fromCodePoint(0x1f600).repeat(EXTERNAL_BILLING_ID_MAX_LENGTH);
    expect(errorsOf({ externalBillingId: value })).not.toHaveProperty('externalBillingId');
  });

  it('rejects an external billing id over the limit in code points', () => {
    const value = String.fromCodePoint(0x1f600).repeat(EXTERNAL_BILLING_ID_MAX_LENGTH + 1);
    expect(errorsOf({ externalBillingId: value })).toHaveProperty('externalBillingId');
  });
});
