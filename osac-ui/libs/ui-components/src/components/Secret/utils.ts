import type { TFunction } from 'i18next';

import { SecretType } from '@osac/types';

export const TYPE_FILTER_VALUES = [
  'kubeconfig',
  'opaque',
  'pullsecret',
  'userdata',
  'value',
] as const;
export type TypeFilterValue = (typeof TYPE_FILTER_VALUES)[number];

export const TYPE_PARAM = 'type';
export const isTypeFilterValue = (value: string): value is TypeFilterValue =>
  TYPE_FILTER_VALUES.includes(value as TypeFilterValue);

export const TYPE_FILTER_TO_ENUM: Record<TypeFilterValue, SecretType> = {
  kubeconfig: SecretType.KUBECONFIG,
  opaque: SecretType.OPAQUE,
  pullsecret: SecretType.PULL_SECRET,
  userdata: SecretType.USER_DATA,
  value: SecretType.VALUE,
};

export const getSecretType = (t: TFunction): Record<SecretType, string> => {
  return {
    [SecretType.UNSPECIFIED]: t('Unspecified'),
    [SecretType.KUBECONFIG]: t('Kubeconfig'),
    [SecretType.OPAQUE]: t('Opaque'),
    [SecretType.PULL_SECRET]: t('Pull secret'),
    [SecretType.USER_DATA]: t('User data'),
    [SecretType.VALUE]: t('Value'),
  };
};

export const downloadSecretBytes = (bytes: Uint8Array, filename: string) => {
  const blobBytes = new ArrayBuffer(bytes.byteLength);
  new Uint8Array(blobBytes).set(bytes);
  const url = URL.createObjectURL(new Blob([blobBytes], { type: 'application/octet-stream' }));
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
  URL.revokeObjectURL(url);
};
