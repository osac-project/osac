import { create } from '@bufbuild/protobuf';
import { describe, expect, it } from 'vitest';

import { SecretSchema, SecretType } from '@osac/types';

import { buildSecretCreatePayload, buildSecretUpdatePayload } from './payload';
import { createSecretDataEntry, getSecretValues } from './values';

const values = (opaque: Array<{ key: string; value: Uint8Array }>) => ({
  metadata: { name: 'my-secret', project: 'my-project', description: 'foo-desc' },
  type: SecretType.OPAQUE,
  kubeconfig: createSecretDataEntry('kubeconfig', new Uint8Array()),
  pullsecret: createSecretDataEntry('.dockerconfigjson', new Uint8Array()),
  userData: createSecretDataEntry('userdata', new Uint8Array()),
  opaque: opaque.map((e) => createSecretDataEntry(e.key, e.value)),
  value: createSecretDataEntry('value', new Uint8Array()),
  sshPublicKey: createSecretDataEntry('public_key', new Uint8Array()),
});

describe('buildSecretCreatePayload', () => {
  it('preserves values for unique secret keys', () => {
    const payload = buildSecretCreatePayload(
      values([
        { key: 'username', value: new TextEncoder().encode('admin') },
        { key: 'password', value: new TextEncoder().encode('s3cret') },
      ]),
    );

    expect(payload.data).toEqual({
      username: new TextEncoder().encode('admin'),
      password: new TextEncoder().encode('s3cret'),
    });
  });

  it('rejects duplicate secret keys before building the payload', () => {
    expect(() =>
      buildSecretCreatePayload(
        values([
          { key: 'username', value: new TextEncoder().encode('admin') },
          { key: 'username', value: new TextEncoder().encode('replacement') },
        ]),
      ),
    ).toThrow('Secret keys must be unique');
  });

  it('uses bytes from a typed secret entry', () => {
    const binaryValue = new Uint8Array([0xff, 0x00, 0x80]);
    const typedValues = values([]);
    typedValues.type = SecretType.VALUE;
    typedValues.value.value = binaryValue;

    const payload = buildSecretCreatePayload(typedValues);

    expect(payload.data?.value).toEqual(binaryValue);
  });

  it('includes public_key data for SSH_PUBLIC_KEY type', () => {
    const keyBytes = new TextEncoder().encode('ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA user@host');
    const typedValues = values([]);
    typedValues.type = SecretType.SSH_PUBLIC_KEY;
    typedValues.sshPublicKey.value = keyBytes;

    const payload = buildSecretCreatePayload(typedValues);

    expect(payload.data).toEqual({ public_key: keyBytes });
    expect(payload.type).toBe(SecretType.SSH_PUBLIC_KEY);
  });
});

describe('buildSecretUpdatePayload', () => {
  it('preserves the original bytes for unchanged text entries', () => {
    const originalValue = new Uint8Array([0xef, 0xbb, 0xbf, 0x61]);
    const secret = create(SecretSchema, {
      id: 'secret-id',
      metadata: { name: 'my-secret', project: 'my-project' },
      data: { certificate: originalValue },
    });
    const values = getSecretValues(null, secret);

    const payload = buildSecretUpdatePayload(values);

    expect(payload.data?.certificate).toEqual(originalValue);
  });

  it('preserves binary entries without a text/file mode', () => {
    const originalValue = new Uint8Array([0xff, 0x00, 0x80]);
    const secret = create(SecretSchema, {
      id: 'secret-id',
      metadata: { name: 'my-secret', project: 'my-project' },
      data: { certificate: originalValue },
    });
    const values = getSecretValues(null, secret);

    expect(values.opaque[0]).toMatchObject({ key: 'certificate', value: originalValue });
    expect(buildSecretUpdatePayload(values).data?.certificate).toEqual(originalValue);
  });

  it('encodes an edited text representation as bytes', () => {
    const secret = create(SecretSchema, {
      id: 'secret-id',
      metadata: { name: 'my-secret', project: 'my-project' },
      data: { username: new TextEncoder().encode('admin') },
    });
    const values = getSecretValues(null, secret);
    values.opaque[0].value = new TextEncoder().encode('updated');

    const payload = buildSecretUpdatePayload(values);

    expect(payload.data?.username).toEqual(new TextEncoder().encode('updated'));
  });
});
