import { Secret, SecretType } from '@osac/types';

import { TYPE_FILTER_TO_ENUM, type TypeFilterValue } from '../utils';

export const SECRET_FILE_MAX_BYTES = 1024 * 1024;

export interface SecretValues {
  metadata: {
    project: string;
    name: string;
    description: string;
  };
  type: SecretType;
  kubeconfig: SecretDataEntry;
  pullsecret: SecretDataEntry;
  userData: SecretDataEntry;
  opaque: SecretDataEntry[];
  value: SecretDataEntry;
}

export interface SecretDataEntry {
  key: string;
  value: Uint8Array;
}

const emptyValue = (): Uint8Array => new Uint8Array();

export const decodeSecretValue = (value: Uint8Array): string | undefined => {
  try {
    return new TextDecoder('utf-8', { fatal: true }).decode(value);
  } catch {
    return undefined;
  }
};

export const encodeSecretValue = (value: string): Uint8Array => new TextEncoder().encode(value);

export const getDataEntries = (data: Secret['data']): SecretDataEntry[] =>
  Object.entries(data).map(([key, value]) => ({
    key,
    value: new Uint8Array(value),
  }));

const getEntry = (data: SecretDataEntry[], key: string): SecretDataEntry => {
  const entry = data.find((d) => d.key === key);

  return (
    entry || {
      key,
      value: emptyValue(),
    }
  );
};

const getDefaultValues = (type: SecretType): SecretValues => ({
  metadata: {
    name: '',
    project: '',
    description: '',
  },
  type,
  kubeconfig: { key: 'kubeconfig', value: emptyValue() },
  pullsecret: { key: '.dockerconfigjson', value: emptyValue() },
  userData: { key: 'userdata', value: emptyValue() },
  opaque: [{ key: '', value: emptyValue() }],
  value: { key: 'value', value: emptyValue() },
});

export const getSecretValues = (
  initType: TypeFilterValue | null,
  secret?: Secret,
): SecretValues => {
  if (!secret) {
    return getDefaultValues(initType ? TYPE_FILTER_TO_ENUM[initType] : SecretType.OPAQUE);
  }

  const type = secret.type === SecretType.UNSPECIFIED ? SecretType.OPAQUE : secret.type;

  const dataEntries = getDataEntries(secret.data);

  return {
    metadata: {
      project: secret.metadata?.project || '',
      name: secret.metadata?.name || '',
      description: secret.metadata?.description || '',
    },
    type,
    kubeconfig: getEntry(dataEntries, 'kubeconfig'),
    pullsecret: getEntry(dataEntries, '.dockerconfigjson'),
    userData: getEntry(dataEntries, 'userdata'),
    opaque: dataEntries,
    value: getEntry(dataEntries, 'value'),
  };
};
