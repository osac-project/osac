import { getOption } from '@bufbuild/protobuf';
import type { TFunction } from 'i18next';

import { StorageBackendSpecSchema, storage_backend_provider_options } from '@osac/types/private';

export const selectableStorageBackendProviders = getOption(
  StorageBackendSpecSchema.field.provider,
  storage_backend_provider_options,
).choices;

export const getStorageBackendProviderLabel = (key: string, t: TFunction): string => {
  const displayName = selectableStorageBackendProviders.find(
    (choice) => choice.key === key,
  )?.displayName;

  switch (displayName) {
    case 'VAST Data':
      return t('VAST Data');
    case 'NetApp ONTAP':
      return t('NetApp ONTAP');
    case 'Pure FlashArray/FlashBlade':
      return t('Pure FlashArray/FlashBlade');
    default:
      return displayName ?? key;
  }
};
