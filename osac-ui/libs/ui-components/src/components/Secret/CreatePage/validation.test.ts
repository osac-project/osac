import { describe, expect, it } from 'vitest';

import { SecretType } from '@osac/types';

import { getSecretValidationSchema } from './validation';
import { SECRET_FILE_MAX_BYTES } from './values';
import { tIdentity as t } from '../../../test-utils/i18n';

const emptyEntry = (key: string) => ({ key, value: new Uint8Array() });

const validValues = {
  metadata: { name: 'my-secret', project: 'my-project' },
  type: SecretType.OPAQUE,
  data: [{ key: 'username', value: new TextEncoder().encode('admin') }],
};

describe('getSecretValidationSchema', () => {
  const schema = getSecretValidationSchema(t, false);

  it('accepts unique secret keys', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        data: [
          { key: 'username', value: new TextEncoder().encode('admin') },
          { key: 'password', value: new TextEncoder().encode('s3cret') },
        ],
      }),
    ).resolves.toBe(true);
  });

  it('rejects duplicate secret keys', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        data: [
          { key: 'username', value: new TextEncoder().encode('admin') },
          { key: 'username', value: new TextEncoder().encode('replacement') },
        ],
      }),
    ).resolves.toBe(false);
  });

  it('preserves required validation for empty keys', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        data: [{ key: '', value: new TextEncoder().encode('admin') }],
      }),
    ).resolves.toBe(false);
  });

  it('accepts binary values', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        data: [{ key: 'certificate', value: new Uint8Array([0xff]) }],
      }),
    ).resolves.toBe(true);
  });

  it('rejects entries without bytes', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        data: [emptyEntry('certificate')],
      }),
    ).resolves.toBe(false);
  });

  it('rejects entries larger than the upload limit', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        data: [{ key: 'certificate', value: new Uint8Array(SECRET_FILE_MAX_BYTES + 1) }],
      }),
    ).resolves.toBe(false);
  });

  it('validates the selected typed entry', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        type: SecretType.VALUE,
        data: [{ key: 'value', value: new Uint8Array([0xff]) }],
      }),
    ).resolves.toBe(true);
  });

  it('requires the selected typed entry', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        type: SecretType.VALUE,
        data: [{ key: 'value', value: new Uint8Array() }],
      }),
    ).resolves.toBe(false);
  });

  it('accepts unique additional keys on typed secrets', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        type: SecretType.VALUE,
        data: [
          { key: 'value', value: new TextEncoder().encode('main') },
          { key: 'token', value: new TextEncoder().encode('abc') },
          { key: 'license', value: new TextEncoder().encode('xyz') },
        ],
      }),
    ).resolves.toBe(true);
  });

  it('rejects duplicate additional keys on typed secrets', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        type: SecretType.VALUE,
        data: [
          { key: 'value', value: new TextEncoder().encode('main') },
          { key: 'token', value: new TextEncoder().encode('abc') },
          { key: 'token', value: new TextEncoder().encode('replacement') },
        ],
      }),
    ).resolves.toBe(false);
  });

  it('rejects additional keys that duplicate the typed required key', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        type: SecretType.KUBECONFIG,
        data: [
          { key: 'kubeconfig', value: new TextEncoder().encode('kube') },
          { key: 'kubeconfig', value: new TextEncoder().encode('duplicate') },
        ],
      }),
    ).resolves.toBe(false);
  });

  it('accepts a valid SSH public key', async () => {
    const key =
      'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGrRFhQBMYKR1oMYGEQ6oldeLBJiqrGGwBIsNpSfXiqu user@host';
    await expect(
      schema.isValid({
        ...validValues,
        type: SecretType.SSH_PUBLIC_KEY,
        data: [{ key: 'public_key', value: new TextEncoder().encode(key) }],
      }),
    ).resolves.toBe(true);
  });

  it('rejects an invalid SSH public key', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        type: SecretType.SSH_PUBLIC_KEY,
        data: [{ key: 'public_key', value: new TextEncoder().encode('not-a-key') }],
      }),
    ).resolves.toBe(false);
  });

  it('rejects SSH public key values that are not valid UTF-8', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        type: SecretType.SSH_PUBLIC_KEY,
        data: [{ key: 'public_key', value: new Uint8Array([0xff, 0xfe, 0x00]) }],
      }),
    ).resolves.toBe(false);
  });

  it('rejects whitespace-only SSH public key values', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        type: SecretType.SSH_PUBLIC_KEY,
        data: [{ key: 'public_key', value: new TextEncoder().encode('   \n\t  ') }],
      }),
    ).resolves.toBe(false);
  });

  it('requires a value when SSH_PUBLIC_KEY is selected', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        type: SecretType.SSH_PUBLIC_KEY,
        data: [{ key: 'public_key', value: new Uint8Array() }],
      }),
    ).resolves.toBe(false);
  });
});
