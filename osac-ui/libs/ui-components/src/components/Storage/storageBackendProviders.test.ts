import { type TFunction } from 'i18next';
import { describe, expect, it } from 'vitest';

import {
  STORAGE_BACKEND_PROVIDER_VALUES,
  getStorageBackendProviderOptions,
} from '@osac/ui-components/components/Storage/storageBackendProviders';

describe('storage backend provider metadata', () => {
  const t = ((key: string) => key) as TFunction;

  it('uses provider keys and English display names from the protobuf descriptor', () => {
    expect(STORAGE_BACKEND_PROVIDER_VALUES).toEqual([
      'vast',
      'netapp',
      'pure-flashblade',
      'pure-flasharray',
    ]);
    expect(getStorageBackendProviderOptions(t)).toEqual([
      { value: 'vast', label: 'VAST Data' },
      { value: 'netapp', label: 'NetApp ONTAP' },
      { value: 'pure-flashblade', label: 'Pure FlashBlade' },
      { value: 'pure-flasharray', label: 'Pure FlashArray' },
    ]);
  });
});
