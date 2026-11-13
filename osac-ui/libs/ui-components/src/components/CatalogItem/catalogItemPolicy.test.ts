import { describe, expect, it } from 'vitest';

import { catalogFieldPolicyFromValue } from './catalogItemPolicy';

describe('catalogFieldPolicyFromValue', () => {
  const isEmptyString = (value: string) => value.trim() === '';

  it('writes the locked arm when the field is locked, even if empty', () => {
    const policy = catalogFieldPolicyFromValue({ value: '', locked: true }, isEmptyString);
    expect(policy).toEqual({ behavior: { case: 'locked', value: '' } });
  });

  it('omits the policy when the field is editable and empty', () => {
    const policy = catalogFieldPolicyFromValue({ value: '', locked: false }, isEmptyString);
    expect(policy).toBeUndefined();
  });

  it('writes the editable arm with a default when the field is editable and non-empty', () => {
    const policy = catalogFieldPolicyFromValue(
      { value: 'cloud-init', locked: false },
      isEmptyString,
    );
    expect(policy).toEqual({
      behavior: { case: 'editable', value: { defaultValue: 'cloud-init' } },
    });
  });

  it('uses the field value directly when no conversion is needed', () => {
    const value = { id: 'image-1', name: 'rhel-9' };
    const policy = catalogFieldPolicyFromValue({ value, locked: true }, (resource) => !resource.id);

    expect(policy).toEqual({ behavior: { case: 'locked', value } });
  });
});
