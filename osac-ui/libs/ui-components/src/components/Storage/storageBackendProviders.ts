import { getOption } from '@bufbuild/protobuf';
import { type TFunction } from 'i18next';

import { StorageBackendSpecSchema, storage_backend_provider_options } from '@osac/types/private';
import type { SelectFieldOption } from '@osac/ui-components/components/Form/SelectField';

const providerField = StorageBackendSpecSchema.fields.find(({ name }) => name === 'provider');

if (!providerField) {
  throw new Error('StorageBackendSpec.provider field is missing from the protobuf descriptor');
}

const providerChoices = getOption(providerField, storage_backend_provider_options).choices;

export const STORAGE_BACKEND_PROVIDER_VALUES = providerChoices.map(({ key }) => key);

// Keep static t() calls so i18next can extract labels supplied by the protobuf catalog.
const translateProviderDisplayName = (t: TFunction, displayName: string): string => {
  const translations: Record<string, string> = {
    'VAST Data': t('VAST Data'),
    'NetApp ONTAP': t('NetApp ONTAP'),
    'Pure FlashBlade': t('Pure FlashBlade'),
    'Pure FlashArray': t('Pure FlashArray'),
  };
  const translatedName = translations[displayName];

  if (!translatedName) {
    throw new Error(`Missing i18n translation for storage provider '${displayName}'`);
  }

  return translatedName;
};

export const getStorageBackendProviderOptions = (t: TFunction): SelectFieldOption[] =>
  providerChoices.map(({ key, displayName }) => ({
    value: key,
    label: translateProviderDisplayName(t, displayName),
  }));

export const getStorageBackendProviderLabel = (t: TFunction, provider?: string) => {
  const option = getStorageBackendProviderOptions(t).find(({ value }) => value === provider);
  return option?.label ?? provider;
};
