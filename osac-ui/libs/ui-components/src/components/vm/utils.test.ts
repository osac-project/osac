import { describe, expect, it } from 'vitest';

import { type InstanceType, InstanceTypeState } from '@osac/types';

import {
  areGpuSpecsEqual,
  formatInstanceTypeDisplayName,
  formatInstanceTypeOptionLabel,
  formatInstanceTypeReviewLabelFromType,
  isDeprecatedInstanceType,
  isGpuCompatibleInstanceType,
} from './utils';

const makeInstanceType = (overrides: Record<string, unknown> = {}): InstanceType =>
  ({
    id: 'standard-4-8',
    metadata: { name: 'Standard 4 vCPU / 8 GiB' },
    spec: { vcpus: 4, memoryGib: 8, state: InstanceTypeState.ACTIVE },
    ...overrides,
  }) as InstanceType;

const a100Gpu = {
  count: 1,
  resourceName: 'nvidia.com/A100',
  pciDeviceSelector: '10DE:20B0',
};

describe('areGpuSpecsEqual', () => {
  it('treats missing GPU specs as equal', () => {
    expect(areGpuSpecsEqual(undefined, undefined)).toBe(true);
  });

  it('rejects when only one side has a GPU', () => {
    expect(areGpuSpecsEqual(a100Gpu, undefined)).toBe(false);
    expect(areGpuSpecsEqual(undefined, a100Gpu)).toBe(false);
  });

  it('requires count, resource name, and PCI selector to match', () => {
    expect(areGpuSpecsEqual(a100Gpu, { ...a100Gpu })).toBe(true);
    expect(areGpuSpecsEqual(a100Gpu, { ...a100Gpu, count: 2 })).toBe(false);
    expect(areGpuSpecsEqual(a100Gpu, { ...a100Gpu, resourceName: 'nvidia.com/Tesla-T4' })).toBe(
      false,
    );
    expect(areGpuSpecsEqual(a100Gpu, { ...a100Gpu, pciDeviceSelector: '10DE:1EB8' })).toBe(false);
  });
});

describe('isGpuCompatibleInstanceType', () => {
  it('allows CPU/memory resize targets that keep the same GPU spec', () => {
    const current = makeInstanceType({
      id: 'u1-gpu-small',
      spec: {
        vcpus: 2,
        memoryGib: 4,
        state: InstanceTypeState.ACTIVE,
        gpu: a100Gpu,
      },
    });
    const compatible = makeInstanceType({
      id: 'u1-gpu-medium',
      metadata: { name: 'u1-gpu-medium' },
      spec: {
        vcpus: 4,
        memoryGib: 8,
        state: InstanceTypeState.ACTIVE,
        gpu: { ...a100Gpu },
      },
    });
    const incompatible = makeInstanceType({
      id: 'u1-gpu-negative',
      metadata: { name: 'u1-gpu-negative' },
      spec: {
        vcpus: 2,
        memoryGib: 4,
        state: InstanceTypeState.ACTIVE,
        gpu: {
          count: 2,
          resourceName: 'nvidia.com/Tesla-T4',
          pciDeviceSelector: '10DE:1EB8',
        },
      },
    });

    expect(isGpuCompatibleInstanceType(current, compatible)).toBe(true);
    expect(isGpuCompatibleInstanceType(current, incompatible)).toBe(false);
    expect(isGpuCompatibleInstanceType(current, makeInstanceType())).toBe(false);
  });
});

describe('formatInstanceTypeOptionLabel', () => {
  it('groups GPU count and resource name under the GPU clause', () => {
    const instanceType = makeInstanceType({
      id: 'u1-gpu-small',
      metadata: { name: 'u1-gpu-small' },
      spec: {
        vcpus: 2,
        memoryGib: 4,
        state: InstanceTypeState.ACTIVE,
        gpu: a100Gpu,
      },
    });

    expect(formatInstanceTypeOptionLabel(instanceType)).toBe(
      'u1-gpu-small — 2 vCPUs · 4 GiB · 1 GPU · nvidia.com/A100',
    );
  });

  it('omits PCI selector from the option label', () => {
    const instanceType = makeInstanceType({
      id: 'gpu-no-selector',
      metadata: { name: 'gpu-no-selector' },
      spec: {
        vcpus: 4,
        memoryGib: 16,
        state: InstanceTypeState.ACTIVE,
        gpu: { count: 1, resourceName: 'nvidia.com/gpu', pciDeviceSelector: '10DE:20B0' },
      },
    });

    expect(formatInstanceTypeOptionLabel(instanceType)).toBe(
      'gpu-no-selector — 4 vCPUs · 16 GiB · 1 GPU · nvidia.com/gpu',
    );
  });
});

describe('formatInstanceTypeDisplayName', () => {
  it('returns em dash when instance type is undefined and no fallback', () => {
    expect(formatInstanceTypeDisplayName(undefined)).toBe('—');
  });

  it('returns fallback id when instance type is undefined', () => {
    expect(formatInstanceTypeDisplayName(undefined, ' (deprecated)', 'custom-type')).toBe(
      'custom-type',
    );
  });

  it('returns name without sizing for resolved instance type', () => {
    expect(formatInstanceTypeDisplayName(makeInstanceType())).toBe('Standard 4 vCPU / 8 GiB');
  });

  it('appends deprecated suffix when instance type is deprecated', () => {
    const instanceType = makeInstanceType({
      spec: {
        $typeName: 'osac.public.v1.InstanceTypeSpec',
        vcpus: 4,
        memoryGib: 8,
        state: InstanceTypeState.DEPRECATED,
        description: '',
      },
    });
    expect(formatInstanceTypeDisplayName(instanceType, ' (deprecated)')).toBe(
      'Standard 4 vCPU / 8 GiB (deprecated)',
    );
    expect(isDeprecatedInstanceType(instanceType)).toBe(true);
  });
});

describe('formatInstanceTypeReviewLabelFromType', () => {
  it('returns em dash when instance type is undefined and no fallback', () => {
    expect(formatInstanceTypeReviewLabelFromType(undefined)).toBe('—');
  });

  it('includes sizing in review label', () => {
    expect(formatInstanceTypeReviewLabelFromType(makeInstanceType())).toBe(
      'Standard 4 vCPU / 8 GiB — 4 vCPUs · 8 GiB',
    );
  });

  it('returns fallback id when instance type is undefined', () => {
    expect(formatInstanceTypeReviewLabelFromType(undefined, ' (deprecated)', 'standard-4-8')).toBe(
      'standard-4-8',
    );
  });
});
