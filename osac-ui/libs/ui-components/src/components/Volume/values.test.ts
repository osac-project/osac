import { describe, expect, it } from 'vitest';

import { VolumeAccessMode } from '@osac/types';

import { getVolumeValues } from './values';
import { emptyResourceSelectValue } from '../Form/resourceSelectValue';

describe('getVolumeValues', () => {
  it('returns empty create values', () => {
    expect(getVolumeValues()).toEqual({
      metadata: {
        name: '',
      },
      spec: {
        storageTier: emptyResourceSelectValue(),
        sizeGib: undefined,
        accessMode: VolumeAccessMode.UNSPECIFIED,
      },
    });
  });
});
