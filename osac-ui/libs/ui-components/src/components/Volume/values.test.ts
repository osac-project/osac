import { describe, expect, it } from 'vitest';

import { VolumeAccessMode } from '@osac/types';

import { getVolumeValues } from './values';
import { emptyResourceSelectValue } from '../Form/resourceSelectValue';

describe('getVolumeValues', () => {
  it('returns empty create values', () => {
    expect(getVolumeValues()).toEqual({
      metadata: {
        name: '',
        description: '',
      },
      spec: {
        storageTier: emptyResourceSelectValue(),
        sizeGib: '',
        accessMode: VolumeAccessMode.UNSPECIFIED,
      },
    });
  });
});
