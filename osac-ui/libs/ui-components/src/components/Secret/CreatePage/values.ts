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
  data: SecretDataEntry[];
}

export interface SecretDataEntry {
  uid: string;
  key: string;
  value: Uint8Array;
}

let nextUid = 0;
export const createSecretDataEntry = (key: string, value: Uint8Array): SecretDataEntry => ({
  uid: `entry-${nextUid++}`,
  key,
  value,
});

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
  Object.entries(data).map(([key, value]) => createSecretDataEntry(key, new Uint8Array(value)));

export const defaultDataForType = (type: SecretType): SecretDataEntry[] => {
  switch (type) {
    case SecretType.OPAQUE:
    case SecretType.UNSPECIFIED:
      return [createSecretDataEntry('', emptyValue())];
    case SecretType.KUBECONFIG:
      return [createSecretDataEntry('kubeconfig', emptyValue())];
    case SecretType.PULL_SECRET:
      return [createSecretDataEntry('.dockerconfigjson', emptyValue())];
    case SecretType.USER_DATA:
      return [createSecretDataEntry('userdata', emptyValue())];
    case SecretType.VALUE:
      return [createSecretDataEntry('value', emptyValue())];
    case SecretType.SSH_PUBLIC_KEY:
      return [createSecretDataEntry('public_key', emptyValue())];
    default:
      return [];
  }
};

const getDefaultValues = (type: SecretType): SecretValues => ({
  metadata: {
    name: '',
    project: '',
    description: '',
  },
  type,
  data: defaultDataForType(type),
});

const sortRequiredKeyFirst = (
  requiredKey: string,
  dataEntries: SecretDataEntry[],
): SecretDataEntry[] => {
  const requiredEntry =
    dataEntries.find((entry) => entry.key === requiredKey) ||
    createSecretDataEntry(requiredKey, emptyValue());

  const additionalEntries = dataEntries.filter((entry) => entry.key !== requiredKey);

  return [requiredEntry, ...additionalEntries];
};

const hydrateDataFromApi = (
  type: SecretType,
  dataEntries: SecretDataEntry[],
): SecretDataEntry[] => {
  switch (type) {
    case SecretType.OPAQUE:
    case SecretType.UNSPECIFIED:
      return dataEntries.length > 0 ? dataEntries : [createSecretDataEntry('', emptyValue())];
    case SecretType.KUBECONFIG:
      return sortRequiredKeyFirst('kubeconfig', dataEntries);
    case SecretType.PULL_SECRET:
      return sortRequiredKeyFirst('.dockerconfigjson', dataEntries);
    case SecretType.USER_DATA:
      return sortRequiredKeyFirst('userdata', dataEntries);
    case SecretType.VALUE:
      return sortRequiredKeyFirst('value', dataEntries);
    case SecretType.SSH_PUBLIC_KEY:
      return sortRequiredKeyFirst('public_key', dataEntries);
    default:
      return [createSecretDataEntry('', emptyValue())];
  }
};

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
    data: hydrateDataFromApi(type, dataEntries),
  };
};
