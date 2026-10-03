import { create } from '@bufbuild/protobuf';
import { describe, expect, it, vi } from 'vitest';

import {
  BareMetalHardwareSpecSchema,
  type BareMetalInstance,
  BareMetalInstanceState,
  BareMetalInstanceTypeSchema,
} from '@osac/types';

import {
  bareMetalSpecFiltersFromQuery,
  buildBareMetalSshCommand,
  copyBareMetalSshCommand,
  filterBareMetalInstancesByPowerState,
  filterBareMetalInstancesBySpecs,
  formatBareMetalCpu,
  formatBareMetalGpu,
  formatBareMetalMemory,
  getBareMetalSpecFilterGroups,
  getBareMetalSpecFilterGroupsFromInstanceTypes,
  inferDiskImageOs,
  resolveBareMetalSpecFilterValues,
} from './bareMetalInstanceDisplay';

describe('bareMetalInstanceDisplay', () => {
  it('formats hardware summary fields', () => {
    const hardware = create(BareMetalHardwareSpecSchema, {
      cpu: { cores: 64, architecture: 'x86_64', model: 'EPYC', threadsPerCore: 2 },
      memory: { totalGb: 512n, type: 'DDR5' },
      accelerators: [{ type: 'GPU', model: 'A100', vendor: 'NVIDIA', memoryGb: 80 }],
      disks: [],
      networkPorts: [],
      capabilities: {},
    });

    expect(formatBareMetalCpu(hardware)).toBe('128 vCPU');
    expect(formatBareMetalMemory(hardware)).toBe('512 GB');
    expect(formatBareMetalGpu(hardware)).toBe('NVIDIA A100 80 GB');
  });

  it('copies the ssh command to the clipboard', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal('navigator', {
      ...navigator,
      clipboard: { writeText },
    });

    await copyBareMetalSshCommand('test-user', '10.0.0.14');

    expect(writeText).toHaveBeenCalledWith(buildBareMetalSshCommand('test-user', '10.0.0.14'));
  });

  it('infers disk image operating systems from the image name', () => {
    expect(inferDiskImageOs('RHEL 9.4')).toBe('rhel');
    expect(inferDiskImageOs('Windows Server 2022')).toBe('windows');
    expect(inferDiskImageOs('Fedora 41')).toBe('linux');
  });

  it('filters bare metal instances by power state', () => {
    const instances = [
      { id: 'running', status: { state: BareMetalInstanceState.RUNNING } },
      { id: 'stopped', status: { state: BareMetalInstanceState.STOPPED } },
      { id: 'starting', status: { state: BareMetalInstanceState.STARTING } },
      { id: 'failed', status: { state: BareMetalInstanceState.FAILED } },
    ] as BareMetalInstance[];

    expect(filterBareMetalInstancesByPowerState([...instances], undefined)).toEqual([...instances]);
    expect(filterBareMetalInstancesByPowerState([...instances], 'running')).toEqual([instances[0]]);
    expect(filterBareMetalInstancesByPowerState([...instances], 'restarting')).toEqual([
      instances[2],
    ]);
    expect(filterBareMetalInstancesByPowerState([...instances], 'failing')).toEqual([instances[3]]);
  });

  it('derives spec filter groups from instance types and disk image names', () => {
    const hardware = create(BareMetalHardwareSpecSchema, {
      cpu: { cores: 16, architecture: 'x86_64', model: 'EPYC', threadsPerCore: 2 },
      memory: { totalGb: 256n, type: 'DDR5' },
      accelerators: [{ type: 'GPU', model: 'A100', vendor: 'NVIDIA', memoryGb: 40 }],
      disks: [],
      networkPorts: [],
      capabilities: {},
    });

    const instanceTypes = [
      create(BareMetalInstanceTypeSchema, {
        id: 'type-1',
        metadata: { name: 'gpu-large' },
        spec: { hardware, description: '' },
      }),
    ];

    expect(
      getBareMetalSpecFilterGroupsFromInstanceTypes(instanceTypes, ['Fedora 41', 'RHEL 9.4']),
    ).toEqual({
      diskImage: ['Fedora 41', 'RHEL 9.4'],
      gpu: ['NVIDIA A100 40 GB'],
      ram: ['256 GB'],
      cpu: ['32 vCPU'],
    });
  });

  it('derives spec filter groups and filters instances by selected specs', () => {
    const hardware = create(BareMetalHardwareSpecSchema, {
      cpu: { cores: 16, architecture: 'x86_64', model: 'EPYC', threadsPerCore: 2 },
      memory: { totalGb: 256n, type: 'DDR5' },
      accelerators: [{ type: 'GPU', model: 'A100', vendor: 'NVIDIA', memoryGb: 40 }],
      disks: [],
      networkPorts: [],
      capabilities: {},
    });

    const instances = [
      {
        id: 'a',
        spec: {
          diskImage: { name: 'Fedora 41' },
          instanceType: { id: 'type-1', name: 'gpu-large' },
        },
      },
      {
        id: 'b',
        spec: {
          diskImage: { name: 'RHEL 9.4' },
          instanceType: { id: 'type-2', name: 'cpu-large' },
        },
      },
    ] as BareMetalInstance[];

    const resolveHardware = () => hardware;
    const groups = getBareMetalSpecFilterGroups(instances, resolveHardware);

    expect(groups).toEqual({
      diskImage: ['Fedora 41', 'RHEL 9.4'],
      gpu: ['NVIDIA A100 40 GB'],
      ram: ['256 GB'],
      cpu: ['32 vCPU'],
    });

    const filters = bareMetalSpecFiltersFromQuery(
      { diskImage: ['Fedora 41'], gpu: [], ram: [], cpu: [] },
      groups,
    );
    expect(resolveBareMetalSpecFilterValues(['missing'], groups.diskImage)).toEqual([]);
    expect(filterBareMetalInstancesBySpecs(instances, filters, resolveHardware)).toEqual([
      instances[0],
    ]);
  });
});
