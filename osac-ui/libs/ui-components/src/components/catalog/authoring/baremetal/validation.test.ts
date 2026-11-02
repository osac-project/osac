import type { TFunction } from 'i18next';
import { describe, expect, it } from 'vitest';

import { buildBareMetalCatalogStepSchema } from './validation';

const t = ((key: string) => key) as TFunction;

describe('buildBareMetalCatalogStepSchema', () => {
  it('rejects whitespace-only user data when locked', async () => {
    const schema = buildBareMetalCatalogStepSchema('configuration', t, 'tenant-admin');

    await expect(
      schema?.validate({
        instanceType: { locked: false, value: '' },
        diskImage: { locked: false, value: { id: '', name: '' } },
        userData: { locked: true, value: '  ' },
      }),
    ).rejects.toMatchObject({
      path: 'userData.value',
      message: 'User data is required when locked',
    });
  });

  it('rejects user data larger than 64 KB', async () => {
    const schema = buildBareMetalCatalogStepSchema('configuration', t, 'tenant-admin');

    await expect(
      schema?.validate({
        userData: { locked: false, value: 'x'.repeat(65537) },
      }),
    ).rejects.toMatchObject({
      path: 'userData.value',
      message: 'Cloud-init must not exceed 64 KB.',
    });
  });
});
