import { create } from '@bufbuild/protobuf';
import { describe, expect, it } from 'vitest';

import { SecretSchema, SecretType } from '@osac/types';

import { buildSecretCreatePayload, buildSecretUpdatePayload } from './payload';
import { createSecretDataEntry, getSecretValues } from './values';

const values = (data: Array<{ key: string; value: Uint8Array }>) => ({
  metadata: { name: 'my-secret', project: 'my-project', description: 'foo-desc' },
  type: SecretType.OPAQUE,
  data: data.map((e) => createSecretDataEntry(e.key, e.value)),
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
    const typedValues = values([{ key: 'value', value: binaryValue }]);
    typedValues.type = SecretType.VALUE;

    const payload = buildSecretCreatePayload(typedValues);

    expect(payload.data?.value).toEqual(binaryValue);
  });

  it('merges additional keys into typed secret data', () => {
    const typedValues = values([
      { key: 'kubeconfig', value: new TextEncoder().encode('kube-content') },
      { key: 'token', value: new TextEncoder().encode('abc') },
    ]);
    typedValues.type = SecretType.KUBECONFIG;

    const payload = buildSecretCreatePayload(typedValues);

    expect(payload.data).toEqual({
      kubeconfig: new TextEncoder().encode('kube-content'),
      token: new TextEncoder().encode('abc'),
    });
  });

  it('includes public_key data for SSH_PUBLIC_KEY type', () => {
    const keyBytes = new TextEncoder().encode('ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA user@host');
    const typedValues = values([{ key: 'public_key', value: keyBytes }]);
    typedValues.type = SecretType.SSH_PUBLIC_KEY;

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
    const formValues = getSecretValues(null, secret);

    const payload = buildSecretUpdatePayload(formValues);

    expect(payload.data?.certificate).toEqual(originalValue);
  });

  it('preserves binary entries without a text/file mode', () => {
    const originalValue = new Uint8Array([0xff, 0x00, 0x80]);
    const secret = create(SecretSchema, {
      id: 'secret-id',
      metadata: { name: 'my-secret', project: 'my-project' },
      data: { certificate: originalValue },
    });
    const formValues = getSecretValues(null, secret);

    expect(formValues.data[0]).toMatchObject({ key: 'certificate', value: originalValue });
    expect(buildSecretUpdatePayload(formValues).data?.certificate).toEqual(originalValue);
  });

  it('splits typed secret data into required entry and additional keys on edit', () => {
    const kubeBytes = new TextEncoder().encode('kube');
    const tokenBytes = new TextEncoder().encode('token');
    const secret = create(SecretSchema, {
      id: 'secret-id',
      metadata: { name: 'my-secret', project: 'my-project' },
      type: SecretType.KUBECONFIG,
      data: { kubeconfig: kubeBytes, token: tokenBytes },
    });
    const formValues = getSecretValues(null, secret);

    expect(formValues.data).toHaveLength(2);
    expect(formValues.data[0].key).toBe('kubeconfig');
    expect(Array.from(formValues.data[0].value)).toEqual(Array.from(kubeBytes));
    expect(formValues.data[1].key).toBe('token');
    expect(Array.from(formValues.data[1].value)).toEqual(Array.from(tokenBytes));
  });

  it('encodes an edited text representation as bytes', () => {
    const secret = create(SecretSchema, {
      id: 'secret-id',
      metadata: { name: 'my-secret', project: 'my-project' },
      data: { username: new TextEncoder().encode('admin') },
    });
    const formValues = getSecretValues(null, secret);
    formValues.data[0].value = new TextEncoder().encode('updated');

    const payload = buildSecretUpdatePayload(formValues);

    expect(payload.data?.username).toEqual(new TextEncoder().encode('updated'));
  });
});
