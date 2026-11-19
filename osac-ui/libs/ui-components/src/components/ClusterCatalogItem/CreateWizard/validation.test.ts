import type { TFunction } from 'i18next';
import { describe, expect, it } from 'vitest';

import { buildClusterCatalogStepSchema } from './validation';

const t = ((key: string) => key) as TFunction;

describe('buildClusterCatalogStepSchema', () => {
  it('requires a value for locked version and node-set fields', async () => {
    const schema = buildClusterCatalogStepSchema('configuration', t, 'tenant-admin');

    await expect(
      schema?.validate({
        version: { locked: true, value: { id: '', name: '' } },
        nodeSets: { locked: false, value: [] },
      }) as Promise<unknown>,
    ).rejects.toThrow('Version is required');

    await expect(
      schema?.validate({
        version: { locked: false, value: { id: '', name: '' } },
        nodeSets: { locked: true, value: [] },
      }) as Promise<unknown>,
    ).rejects.toThrow('At least one node set');
  });

  it('allows empty editable fields but rejects overlapping CIDRs', async () => {
    const configurationSchema = buildClusterCatalogStepSchema('configuration', t, 'tenant-admin');
    await expect(
      configurationSchema?.validate({
        version: { locked: false, value: { id: '', name: '' } },
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

  it('rejects duplicate node-set names', async () => {
    const schema = buildClusterCatalogStepSchema('configuration', t, 'tenant-admin');

    await expect(
      schema?.validate({
        version: { locked: false, value: { id: '', name: '' } },
        nodeSets: {
          locked: false,
          value: [
            {
              rowId: 'first',
              name: 'workers',
              hostType: { id: 'standard', name: 'standard' },
              size: 3,
            },
            {
              rowId: 'second',
              name: ' workers ',
              hostType: { id: 'standard', name: 'standard' },
              size: 3,
            },
          ],
        },
      }),
    ).rejects.toMatchObject({
      path: 'nodeSets.value[1].name',
      message: 'Node set names must be unique',
    });
  });

  it('requires node-set sizes to be positive integers', async () => {
    const schema = buildClusterCatalogStepSchema('configuration', t, 'tenant-admin');

    await expect(
      schema?.validate({
        version: { locked: false, value: { id: '', name: '' } },
        nodeSets: {
          locked: false,
          value: [
            {
              rowId: 'workers',
              name: 'workers',
              hostType: { id: 'standard', name: 'standard' },
              size: 1.5,
            },
          ],
        },
      }),
    ).rejects.toMatchObject({
      path: 'nodeSets.value[0].size',
      message: 'Must be a whole number',
    });
  });
});
