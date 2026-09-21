import type { MessageInitShape } from '@bufbuild/protobuf';

import { SecretSchema, SecretType } from '@osac/types';

import type { SecretDataEntry, SecretValues } from './values';

const getEntryBytes = (entry: SecretDataEntry): Uint8Array => entry.value;

const buildOpaqueData = (entries: SecretDataEntry[]) => {
  const keys = entries.map(({ key }) => key);
  if (new Set(keys).size !== keys.length) {
    throw new Error('Secret keys must be unique');
  }

  return Object.fromEntries(entries.map((entry) => [entry.key, getEntryBytes(entry)]));
};

const buildTypedData = (values: SecretValues) => {
  switch (values.type) {
    case SecretType.KUBECONFIG:
      return { kubeconfig: getEntryBytes(values.kubeconfig) };
    case SecretType.PULL_SECRET:
      return { '.dockerconfigjson': getEntryBytes(values.pullsecret) };
    case SecretType.USER_DATA:
      return { userdata: getEntryBytes(values.userData) };
    case SecretType.VALUE:
      return { value: getEntryBytes(values.value) };
    case SecretType.OPAQUE:
    case SecretType.UNSPECIFIED:
      return buildOpaqueData(values.opaque);
  }
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
  data: buildTypedData(values),
});

export const buildSecretUpdatePayload = (
  values: SecretValues,
): MessageInitShape<typeof SecretSchema> => ({
  metadata: {
    description: values.metadata.description,
  },
  data: buildTypedData(values),
});
