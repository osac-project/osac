import { describe, expect, it } from 'vitest';

import { createEmptyComputeInstanceValues } from './fields';

describe('createEmptyComputeInstanceValues', () => {
  it('leaves the guest OS family undefined until the disk image field initializes it', () => {
    expect(createEmptyComputeInstanceValues().spec.guestOsFamily).toBeUndefined();
  });
});
