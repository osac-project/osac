import { describe, expect, it } from 'vitest';

import {
  type CatalogFieldPolicyLike,
  catalogValueFromPolicy,
  policyFromCatalogValue,
} from './catalogItemPolicy';

const asString = (value: unknown) => String(value);

describe('catalogValueFromPolicy', () => {
  it('decodes an absent policy as unlocked and empty', () => {
    expect(catalogValueFromPolicy(undefined, asString, '')).toEqual({ value: '', locked: false });
  });

  it('decodes an editable policy with no default as unlocked and empty', () => {
    const policy: CatalogFieldPolicyLike = { behavior: { case: 'editable', value: {} } };
    expect(catalogValueFromPolicy(policy, asString, '')).toEqual({ value: '', locked: false });
  });

  it('decodes a locked string policy as locked', () => {
    const policy: CatalogFieldPolicyLike = { behavior: { case: 'locked', value: 'cloud-init' } };
    expect(catalogValueFromPolicy(policy, asString, '')).toEqual({
      value: 'cloud-init',
      locked: true,
    });
  });

  it('decodes an editable int32 policy with a default', () => {
    const policy: CatalogFieldPolicyLike = {
      behavior: { case: 'editable', value: { defaultValue: 30 } },
    };
    expect(catalogValueFromPolicy(policy, asString, '')).toEqual({ value: '30', locked: false });
  });

  it('decodes a locked resource-reference policy', () => {
    const policy: CatalogFieldPolicyLike = {
      behavior: { case: 'locked', value: { id: 'img-1', name: 'rhel-9' } },
    };
    const readRef = (value: unknown) => {
      const ref = value as { id: string; name: string };
      return { id: ref.id, name: ref.name };
    };
    expect(catalogValueFromPolicy(policy, readRef, { id: '', name: '' })).toEqual({
      value: { id: 'img-1', name: 'rhel-9' },
      locked: true,
    });
  });

  it('decodes an editable disk-list policy with a default', () => {
    const policy: CatalogFieldPolicyLike = {
      behavior: { case: 'editable', value: { defaultValue: { items: [{ sizeGib: 20 }] } } },
    };
    const readItems = (value: unknown) => (value as { items: { sizeGib: number }[] }).items;
    expect(catalogValueFromPolicy(policy, readItems, [])).toEqual({
      value: [{ sizeGib: 20 }],
      locked: false,
    });
  });

  it('decodes a locked node-set map policy', () => {
    const policy: CatalogFieldPolicyLike = {
      behavior: {
        case: 'locked',
        value: { items: { workers: { size: 3, baremetalInstanceType: 'bm-large' } } },
      },
    };
    const readNames = (value: unknown) => Object.keys((value as { items: object }).items);
    expect(catalogValueFromPolicy(policy, readNames, [])).toEqual({
      value: ['workers'],
      locked: true,
    });
  });
});

type StringFieldPolicy =
  | { behavior: { case: 'locked'; value: unknown } }
  | { behavior: { case: 'editable'; value: { defaultValue: unknown } } };

describe('policyFromCatalogValue', () => {
  const isEmptyString = (value: unknown) => (value as string).trim() === '';
  const toLocked = (value: unknown): StringFieldPolicy => ({
    behavior: { case: 'locked', value },
  });
  const toEditable = (value: unknown): StringFieldPolicy => ({
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
