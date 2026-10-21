import type { TFunction } from 'i18next';
import { describe, expect, it } from 'vitest';

import { buildClusterCatalogStepSchema } from './validation';

const t = ((key: string) => key) as TFunction;

describe('buildClusterCatalogStepSchema', () => {
  it('requires a value for locked version and node-set fields', async () => {
    const schema = buildClusterCatalogStepSchema('configuration', t, 'tenant-admin');

    await expect(
      schema?.validate({
        version: { locked: true, value: '' },
        nodeSets: { locked: false, value: [] },
      }),
    ).rejects.toMatchObject({ message: expect.stringContaining('Version is required') });

    await expect(
      schema?.validate({
        version: { locked: false, value: '' },
        nodeSets: { locked: true, value: [] },
      }),
    ).rejects.toMatchObject({ message: expect.stringContaining('At least one node set') });
  });

  it('allows empty editable fields but rejects overlapping CIDRs', async () => {
    const configurationSchema = buildClusterCatalogStepSchema('configuration', t, 'tenant-admin');
    await expect(
      configurationSchema?.validate({
        version: { locked: false, value: '' },
        nodeSets: { locked: false, value: [] },
      }),
    ).resolves.toBeDefined();

    const networkingSchema = buildClusterCatalogStepSchema('networking', t, 'tenant-admin');
    await expect(
      networkingSchema?.validate({
        podCidr: { locked: false, value: '10.128.0.0/14' },
        serviceCidr: { locked: false, value: '10.128.0.0/16' },
      }),
    ).rejects.toMatchObject({ message: 'Service CIDR must not overlap the pod CIDR.' });
  });
});
