import { describe, expect, it } from 'vitest';

import { type InstanceType, InstanceTypeState } from '@osac/types';

import {
  formatInstanceTypeDisplayName,
  formatInstanceTypeReviewLabelFromType,
  isDeprecatedInstanceType,
} from './utils';

const makeInstanceType = (overrides: Partial<InstanceType> = {}): InstanceType =>
  ({
    id: 'standard-4-8',
    metadata: { name: 'Standard 4 vCPU / 8 GiB' },
    spec: { vcpus: 4, memoryGib: 8, state: InstanceTypeState.ACTIVE },
    ...overrides,
  }) as InstanceType;

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
      'Standard 4 vCPU / 8 GiB — 4 vCPUs, 8 GiB',
    );
  });

  it('returns fallback id when instance type is undefined', () => {
    expect(formatInstanceTypeReviewLabelFromType(undefined, ' (deprecated)', 'standard-4-8')).toBe(
      'standard-4-8',
    );
  });
});
