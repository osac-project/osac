import { create } from '@bufbuild/protobuf';
import { describe, expect, it } from 'vitest';

import {
  BareMetalHardwareSpecSchema,
  ExternalIPAttachmentSchema,
  ExternalIPAttachmentState,
} from '@osac/types';

import {
  buildBareMetalSshHostByInstanceId,
  formatBareMetalCpu,
  formatBareMetalGpu,
  formatBareMetalMemory,
  getHostFromAttachments,
  inferDiskImageOs,
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

  it('infers disk image operating systems from the image name', () => {
    expect(inferDiskImageOs('RHEL 9.4')).toBe('rhel');
    expect(inferDiskImageOs('Windows Server 2022')).toBe('windows');
    expect(inferDiskImageOs('Fedora 41')).toBe('linux');
  });

  it('resolves external IP from ready attachments', () => {
    const attachments = [
      create(ExternalIPAttachmentSchema, {
        status: {
          state: ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_PENDING,
          externalIpAddress: '203.0.113.1',
        },
      }),
      create(ExternalIPAttachmentSchema, {
        metadata: { labels: { 'osac.openshift.io/auto-created': 'true' } },
        status: {
          state: ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_READY,
          externalIpAddress: '203.0.113.23',
        },
      }),
    ];

    expect(getHostFromAttachments(attachments)).toBe('203.0.113.23');
  });

  it('indexes SSH hosts by bare metal instance id', () => {
    const attachments = [
      create(ExternalIPAttachmentSchema, {
        spec: {
          externalIp: { id: 'eip-1' },
          target: { case: 'baremetalInstance', value: { id: 'bmi-1' } },
        },
        status: {
          state: ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_READY,
          externalIpAddress: '203.0.113.10',
        },
      }),
      create(ExternalIPAttachmentSchema, {
        spec: {
          externalIp: { id: 'eip-2' },
          target: { case: 'baremetalInstance', value: { id: 'bmi-2' } },
        },
        status: {
          state: ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_READY,
          externalIpAddress: '203.0.113.20',
        },
      }),
    ];

    expect(buildBareMetalSshHostByInstanceId(attachments)).toEqual(
      new Map([
        ['bmi-1', '203.0.113.10'],
        ['bmi-2', '203.0.113.20'],
      ]),
    );
  });
});
