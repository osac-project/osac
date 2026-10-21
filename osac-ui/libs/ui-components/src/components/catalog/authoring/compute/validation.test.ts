import type { TFunction } from 'i18next';
import { describe, expect, it } from 'vitest';

import { buildComputeCatalogStepSchema } from './validation';

const t = ((key: string) => key) as TFunction;

describe('buildComputeCatalogStepSchema', () => {
  it.each([
    ['diskImage', 'Disk image is required when locked'],
    ['instanceType', 'Instance type is required when locked'],
  ])('requires an empty locked %s selection', async (field, message) => {
    const schema = buildComputeCatalogStepSchema('configuration', t, 'tenant-admin');

    await expect(
      schema?.validate({
        [field]: { locked: true, value: { id: '', name: '' } },
      }),
    ).rejects.toMatchObject({ path: `${field}.value.id`, message });
  });

  it('allows an empty editable selection', async () => {
    const schema = buildComputeCatalogStepSchema('configuration', t, 'tenant-admin');

    await expect(
      schema?.validate({
        diskImage: { locked: false, value: { id: '', name: '' } },
        instanceType: { locked: false, value: { id: '', name: '' } },
      }),
    ).resolves.toBeDefined();
  });
});
