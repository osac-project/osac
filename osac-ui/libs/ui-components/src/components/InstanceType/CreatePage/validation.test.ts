import { describe, expect, it } from 'vitest';

import { getInstanceTypeCreateSchema } from './validation';
import { tIdentity as t } from '../../../test-utils/i18n';

const validValues = {
  metadata: { name: 'gp-small' },
  spec: { description: '', vcpus: '4', memoryGib: '16' },
};

describe('getInstanceTypeCreateSchema', () => {
  const schema = getInstanceTypeCreateSchema(t);

  it('accepts valid values', async () => {
    await expect(schema.isValid(validValues)).resolves.toBe(true);
  });

  it('accepts a non-empty description', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        spec: { ...validValues.spec, description: 'General purpose' },
      }),
    ).resolves.toBe(true);
  });

  it('rejects a blank name', async () => {
    await expect(schema.isValid({ ...validValues, metadata: { name: '' } })).resolves.toBe(false);
  });

  it.each([
    ['blank', ''],
    ['decimal', '1.5'],
    ['zero', '0'],
    ['negative', '-1'],
    ['non-numeric', 'abc'],
  ])('rejects %s vcpus', async (_label, vcpus) => {
    await expect(
      schema.isValid({ ...validValues, spec: { ...validValues.spec, vcpus } }),
    ).resolves.toBe(false);
  });

  it.each([
    ['blank', ''],
    ['decimal', '1.5'],
    ['zero', '0'],
    ['negative', '-1'],
    ['non-numeric', 'abc'],
  ])('rejects %s memoryGib', async (_label, memoryGib) => {
    await expect(
      schema.isValid({ ...validValues, spec: { ...validValues.spec, memoryGib } }),
    ).resolves.toBe(false);
  });

  it('accepts positive integer vcpus and memoryGib at the boundary of 1', async () => {
    await expect(
      schema.isValid({ ...validValues, spec: { ...validValues.spec, vcpus: '1', memoryGib: '1' } }),
    ).resolves.toBe(true);
  });

  it('accepts vcpus and memoryGib at the int32 max boundary', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        spec: { ...validValues.spec, vcpus: '2147483647', memoryGib: '2147483647' },
      }),
    ).resolves.toBe(true);
  });

  it('rejects vcpus and memoryGib exceeding the int32 max', async () => {
    await expect(
      schema.isValid({
        ...validValues,
        spec: { ...validValues.spec, vcpus: '2147483648', memoryGib: '2147483648' },
      }),
    ).resolves.toBe(false);
  });

  describe('gpu', () => {
    it('accepts values with no gpu fields at all', async () => {
      await expect(schema.isValid(validValues)).resolves.toBe(true);
    });

    it('accepts a fully populated gpu', async () => {
      await expect(
        schema.isValid({
          ...validValues,
          spec: {
            ...validValues.spec,
            gpu: { pciDeviceSelector: '10DE:20B0', resourceName: 'nvidia.com/A100', count: '2' },
          },
        }),
      ).resolves.toBe(true);
    });

    it.each([
      ['pciDeviceSelector', { pciDeviceSelector: '', resourceName: 'nvidia.com/A100', count: '2' }],
      ['resourceName', { pciDeviceSelector: '10DE:20B0', resourceName: '', count: '2' }],
      ['count', { pciDeviceSelector: '10DE:20B0', resourceName: 'nvidia.com/A100', count: '' }],
    ])('rejects a partially filled gpu missing %s', async (_label, gpu) => {
      await expect(
        schema.isValid({ ...validValues, spec: { ...validValues.spec, gpu } }),
      ).resolves.toBe(false);
    });

    it.each([
      ['zero', '0'],
      ['negative', '-1'],
      ['decimal', '1.5'],
      ['non-numeric', 'abc'],
    ])('rejects an invalid gpu count of %s', async (_label, count) => {
      await expect(
        schema.isValid({
          ...validValues,
          spec: {
            ...validValues.spec,
            gpu: { pciDeviceSelector: '10DE:20B0', resourceName: 'nvidia.com/A100', count },
          },
        }),
      ).resolves.toBe(false);
    });

    it('accepts a large gpu count, deferring the upper bound to the backend', async () => {
      await expect(
        schema.isValid({
          ...validValues,
          spec: {
            ...validValues.spec,
            gpu: { pciDeviceSelector: '10DE:20B0', resourceName: 'nvidia.com/A100', count: '1' },
          },
        }),
      ).resolves.toBe(true);
      await expect(
        schema.isValid({
          ...validValues,
          spec: {
            ...validValues.spec,
            gpu: { pciDeviceSelector: '10DE:20B0', resourceName: 'nvidia.com/A100', count: '9999' },
          },
        }),
      ).resolves.toBe(true);
    });
  });
});
