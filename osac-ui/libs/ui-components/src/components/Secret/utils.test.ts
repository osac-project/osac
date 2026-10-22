import { describe, expect, it } from 'vitest';

import { SecretType } from '@osac/types';

import { TYPE_FILTER_TO_ENUM, TYPE_FILTER_VALUES, getSecretType, isTypeFilterValue } from './utils';
import { tIdentity as t } from '../../test-utils/i18n';

describe('Secret type filter', () => {
  it('includes sshpublickey in the filter values', () => {
    expect(TYPE_FILTER_VALUES).toContain('sshpublickey');
  });

  it('maps sshpublickey to SecretType.SSH_PUBLIC_KEY', () => {
    expect(TYPE_FILTER_TO_ENUM.sshpublickey).toBe(SecretType.SSH_PUBLIC_KEY);
  });

  it('recognises sshpublickey as a valid filter value', () => {
    expect(isTypeFilterValue('sshpublickey')).toBe(true);
  });

  it('returns "SSH public key" as the display label', () => {
    const labels = getSecretType(t);
    expect(labels[SecretType.SSH_PUBLIC_KEY]).toBe('SSH public key');
  });
});
