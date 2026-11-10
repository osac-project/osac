import { describe, expect, it } from 'vitest';

import { isValidSshPublicKey } from './credentialValidation';

describe('isValidSshPublicKey', () => {
  it('accepts empty values', () => {
    expect(isValidSshPublicKey('')).toBe(true);
    expect(isValidSshPublicKey(undefined)).toBe(true);
  });

  it('accepts supported key types', () => {
    expect(
      isValidSshPublicKey(
        'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBJACfzqANDyW1ygNn0FWP7YBZ6XLt+XPGpSw5PyknOW user@host',
      ),
    ).toBe(true);
  });

  it('rejects malformed keys', () => {
    expect(isValidSshPublicKey('not-a-key')).toBe(false);
  });
});
