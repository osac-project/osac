import { describe, expect, it } from 'vitest';

import { VolumeAccessMode } from '@osac/types';

import { buildVolumeCreatePayload } from './payload';
import type { VolumeFormValues } from './values';

const values: VolumeFormValues = {
  metadata: {
    name: 'data-volume',
    description: 'A block volume',
  },
  spec: {
    storageTier: { id: 'gold-block', name: 'gold-block' },
    sizeGib: '128',
    accessMode: VolumeAccessMode.READ_WRITE_ONCE,
  },
};

describe('buildVolumeCreatePayload', () => {
  it('maps form values to the Volume create object', () => {
    const payload = buildVolumeCreatePayload(values);

    expect(payload).toEqual({
      metadata: {
        name: 'data-volume',
        description: 'A block volume',
      },
      spec: {
        storageTier: 'gold-block',
        sizeGib: 128n,
        accessMode: VolumeAccessMode.READ_WRITE_ONCE,
      },
    });
    expect(payload.metadata).not.toHaveProperty('project');
  });
});
