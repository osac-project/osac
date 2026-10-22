import { describe, expect, it } from 'vitest';

import { isValidSshPublicKey, trimSshPublicKey } from './credentialValidation';

describe('trimSshPublicKey', () => {
  it('trims lines and removes empty rows', () => {
    expect(trimSshPublicKey('  ssh-ed25519 AAAA  user@host\n\n')).toBe(
      'ssh-ed25519 AAAA  user@host',
    );
  });
});

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
