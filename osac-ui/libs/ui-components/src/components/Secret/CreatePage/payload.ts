import type { MessageInitShape } from '@bufbuild/protobuf';

import { SecretSchema } from '@osac/types';

import type { SecretDataEntry, SecretValues } from './values';

const getEntryBytes = (entry: SecretDataEntry): Uint8Array => entry.value;

const buildSecretData = (entries: SecretDataEntry[]) => {
  const keys = entries.map(({ key }) => key);
  if (new Set(keys).size !== keys.length) {
    throw new Error('Secret keys must be unique');
  }

  return Object.fromEntries(entries.map((entry) => [entry.key, getEntryBytes(entry)]));
};

export const buildSecretCreatePayload = (
  values: SecretValues,
): MessageInitShape<typeof SecretSchema> => ({
  metadata: {
    name: values.metadata.name,
    project: values.metadata.project,
    description: values.metadata.description,
  },
  type: values.type,
  data: buildSecretData(values.data),
});

export const buildSecretUpdatePayload = (
  values: SecretValues,
): MessageInitShape<typeof SecretSchema> => ({
  metadata: {
    description: values.metadata.description,
  },
  data: buildSecretData(values.data),
});
