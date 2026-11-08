import { describe, expect, it } from 'vitest';

import { getFieldFromCatalogItemPolicy } from './provisioningFieldValue';

describe('getFieldFromCatalogItemPolicy', () => {
  it('returns the default value for an absent policy', () => {
    expect(getFieldFromCatalogItemPolicy(undefined, '')).toEqual({ value: '', locked: false });
  });

  it('returns the default value for an editable policy without a default', () => {
    expect(
      getFieldFromCatalogItemPolicy({ behavior: { case: 'editable', value: {} } }, ''),
    ).toEqual({ value: '', locked: false });
  });

  it('returns the locked policy value and lock state', () => {
    expect(
      getFieldFromCatalogItemPolicy({ behavior: { case: 'locked', value: 'cloud-init' } }, ''),
    ).toEqual({ value: 'cloud-init', locked: true });
  });

  it('returns the editable default and unlocked state', () => {
    expect(
      getFieldFromCatalogItemPolicy(
        { behavior: { case: 'editable', value: { defaultValue: 30 } } },
        0,
      ),
    ).toEqual({ value: 30, locked: false });
  });
});
