import { describe, expect, it } from 'vitest';

import { policyFromCatalogValue } from './catalogItemPolicy';

type StringFieldPolicy<TValue> =
  | { behavior: { case: 'locked'; value: TValue } }
  | { behavior: { case: 'editable'; value: { defaultValue: TValue } } };

describe('policyFromCatalogValue', () => {
  const isEmptyString = (value: string) => value.trim() === '';
  const toLocked = (value: string): StringFieldPolicy<string> => ({
    behavior: { case: 'locked', value },
  });
  const toEditable = (value: string): StringFieldPolicy<string> => ({
    behavior: { case: 'editable', value: { defaultValue: value } },
  });

  it('writes the locked arm when the field is locked, even if empty', () => {
    const policy = policyFromCatalogValue(
      { value: '', locked: true },
      isEmptyString,
      toLocked,
      toEditable,
    );
    expect(policy).toEqual({ behavior: { case: 'locked', value: '' } });
  });

  it('omits the policy when the field is editable and empty', () => {
    const policy = policyFromCatalogValue(
      { value: '', locked: false },
      isEmptyString,
      toLocked,
      toEditable,
    );
    expect(policy).toBeUndefined();
  });

  it('writes the editable arm with a default when the field is editable and non-empty', () => {
    const policy = policyFromCatalogValue(
      { value: 'cloud-init', locked: false },
      isEmptyString,
      toLocked,
      toEditable,
    );
    expect(policy).toEqual({
      behavior: { case: 'editable', value: { defaultValue: 'cloud-init' } },
    });
  });
});
