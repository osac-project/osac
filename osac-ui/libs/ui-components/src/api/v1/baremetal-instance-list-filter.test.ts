import { create } from '@bufbuild/protobuf';
import { describe, expect, it } from 'vitest';

import {
  BareMetalHardwareSpecSchema,
  type BareMetalInstance,
  BareMetalInstanceState,
  BareMetalInstanceTypeSchema,
} from '@osac/types';

import {
  bareMetalInstanceMatchesListFilter,
  buildBareMetalListFilter,
} from './baremetal-instance-list-filter';

const gpuInstanceType = create(BareMetalInstanceTypeSchema, {
  id: 'type-gpu',
  metadata: { name: 'gpu-large' },
  spec: {
    description: 'GPU host',
    hardware: create(BareMetalHardwareSpecSchema, {
      cpu: { cores: 16, architecture: 'x86_64', model: 'EPYC', threadsPerCore: 2 },
      memory: { totalGb: 256n, type: 'DDR5' },
      accelerators: [{ type: 'GPU', model: 'A100', vendor: 'NVIDIA', memoryGb: 40 }],
      disks: [],
      networkPorts: [],
      capabilities: {},
    }),
  },
});

describe('buildBareMetalListFilter', () => {
  it.each([
    ['no criteria', {}, undefined],
    [
      'search only',
      { search: 'worker' },
      '(this.metadata.name.contains("worker") || this.id.contains("worker"))',
    ],
    [
      'power state only',
      { powerState: 'running' as const },
      `this.status.state == ${BareMetalInstanceState.RUNNING}`,
    ],
    [
      'disk image only',
      { specs: { diskImage: ['Fedora 41'], gpu: [], ram: [], cpu: [] } },
      'this.spec.disk_image.name in ["Fedora 41"]',
    ],
    [
      'search and power state',
      { search: 'worker', powerState: 'stopped' as const },
      `(this.metadata.name.contains("worker") || this.id.contains("worker")) && this.status.state == ${BareMetalInstanceState.STOPPED}`,
    ],
    [
      'search escapes quotes and backslashes',
      { search: 'a"b\\c' },
      '(this.metadata.name.contains("a\\"b\\\\c") || this.id.contains("a\\"b\\\\c"))',
    ],
    ['whitespace-only search', { search: '   ' }, undefined],
    [
      'gpu spec filter via instance types',
      { specs: { diskImage: [], gpu: ['NVIDIA A100 40 GB'], ram: [], cpu: [] } },
      '(this.spec.instance_type.id in ["type-gpu"] || this.spec.instance_type.name in ["gpu-large"])',
      [gpuInstanceType],
    ],
    [
      'no matching instance types for hardware filter',
      { specs: { diskImage: [], gpu: ['missing gpu'], ram: [], cpu: [] } },
      'false',
      [gpuInstanceType],
    ],
  ])('%s', (_name, criteria, expected, instanceTypes = []) => {
    expect(buildBareMetalListFilter(criteria, instanceTypes)).toBe(expected);
  });
});

describe('bareMetalInstanceMatchesListFilter', () => {
  const instance = {
    id: 'bmi-1',
    metadata: { name: 'worker-01', project: 'default' },
    spec: { diskImage: { name: 'RHEL 9.4' } },
    status: { state: BareMetalInstanceState.RUNNING },
  } as BareMetalInstance;

  it('matches when filter is undefined', () => {
    expect(bareMetalInstanceMatchesListFilter(instance, undefined)).toBe(true);
  });

  it('matches search and power state clauses', () => {
    const filter = buildBareMetalListFilter({ search: 'worker', powerState: 'running' });
    expect(bareMetalInstanceMatchesListFilter(instance, filter)).toBe(true);
    expect(
      bareMetalInstanceMatchesListFilter(
        instance,
        buildBareMetalListFilter({ powerState: 'stopped' }),
      ),
    ).toBe(false);
  });

  it('matches disk image clauses', () => {
    expect(
      bareMetalInstanceMatchesListFilter(
        instance,
        buildBareMetalListFilter({
          specs: { diskImage: ['RHEL 9.4'], gpu: [], ram: [], cpu: [] },
        }),
      ),
    ).toBe(true);
    expect(
      bareMetalInstanceMatchesListFilter(
        instance,
        buildBareMetalListFilter({
          specs: { diskImage: ['Fedora 41'], gpu: [], ram: [], cpu: [] },
        }),
      ),
    ).toBe(false);
  });

  it('matches hardware filters built from instance types', () => {
    const instanceWithType = {
      ...instance,
      spec: {
        ...instance.spec,
        instanceType: { id: 'type-gpu', name: 'gpu-large' },
      },
    } as BareMetalInstance;
    const filter = buildBareMetalListFilter(
      { specs: { diskImage: [], gpu: ['NVIDIA A100 40 GB'], ram: [], cpu: [] } },
      [gpuInstanceType],
    );

    expect(bareMetalInstanceMatchesListFilter(instanceWithType, filter)).toBe(true);
    expect(
      bareMetalInstanceMatchesListFilter(
        {
          ...instanceWithType,
          spec: { ...instanceWithType.spec, instanceType: { id: 'other' } },
        } as BareMetalInstance,
        filter,
      ),
    ).toBe(false);
  });

  it('rejects all instances when the filter is false', () => {
    expect(bareMetalInstanceMatchesListFilter(instance, 'false')).toBe(false);
  });

  it('matches escaped search terms', () => {
    const quotedInstance = {
      ...instance,
      metadata: { name: 'worker"a\\name', project: 'default' },
    } as BareMetalInstance;
    const filter = buildBareMetalListFilter({ search: 'a"b\\c' });

    expect(bareMetalInstanceMatchesListFilter(quotedInstance, filter)).toBe(false);
    expect(
      bareMetalInstanceMatchesListFilter(
        {
          ...quotedInstance,
          metadata: { name: 'prefix a"b\\c suffix', project: 'default' },
        } as BareMetalInstance,
        filter,
      ),
    ).toBe(true);
  });
});
