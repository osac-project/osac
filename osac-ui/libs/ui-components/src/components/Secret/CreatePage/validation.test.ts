import { describe, expect, it } from 'vitest';

import { SecretType } from '@osac/types';

import { getSecretValidationSchema } from './validation';
import { SECRET_FILE_MAX_BYTES } from './values';
import { tIdentity as t } from '../../../test-utils/i18n';

const emptyEntry = (key: string) => ({ key, value: new Uint8Array() });

const validValues = {
  metadata: { name: 'my-secret', project: 'my-project' },
  type: SecretType.OPAQUE,
  kubeconfig: emptyEntry('kubeconfig'),
  pullsecret: emptyEntry('.dockerconfigjson'),
  userData: emptyEntry('userdata'),
  opaque: [{ key: 'username', value: new TextEncoder().encode('admin') }],
  value: emptyEntry('value'),
};

describe('getSecretValidationSchema', () => {
  const schema = getSecretValidationSchema(t, false);

  it('accepts unique secret keys', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        opaque: [
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
        opaque: [
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
        opaque: [{ key: '', value: new TextEncoder().encode('admin') }],
      }),
    ).resolves.toBe(false);
  });

  it('accepts binary values', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        opaque: [{ key: 'certificate', value: new Uint8Array([0xff]) }],
      }),
    ).resolves.toBe(true);
  });

  it('rejects entries without bytes', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        opaque: [emptyEntry('certificate')],
      }),
    ).resolves.toBe(false);
  });

  it('rejects entries larger than the upload limit', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        opaque: [{ key: 'certificate', value: new Uint8Array(SECRET_FILE_MAX_BYTES + 1) }],
      }),
    ).resolves.toBe(false);
  });

  it('validates the selected typed entry', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        type: SecretType.VALUE,
        value: { key: 'value', value: new Uint8Array([0xff]) },
      }),
    ).resolves.toBe(true);
  });

  it('requires the selected typed entry', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        type: SecretType.VALUE,
      }),
    ).resolves.toBe(false);
  });
});
