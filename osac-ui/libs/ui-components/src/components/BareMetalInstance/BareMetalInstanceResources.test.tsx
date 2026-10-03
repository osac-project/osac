import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { type BareMetalInstance, BareMetalInstanceTypeSchema } from '@osac/types';

import BareMetalInstanceResources from './BareMetalInstanceResources';
import { renderWithProviders } from '../../test-utils/TestProviders';

const instanceType = create(BareMetalInstanceTypeSchema, {
  id: 'gpu-large',
  metadata: { name: 'gpu-large' },
  spec: {
    hardware: {
      cpu: { cores: 64, architecture: 'x86_64', model: 'EPYC', threadsPerCore: 1 },
      memory: { totalGb: 512n, type: 'DDR5' },
      accelerators: [{ type: 'GPU', model: 'A100', vendor: 'NVIDIA', memoryGb: 80 }],
      disks: [],
      networkPorts: [],
      capabilities: {},
    },
  },
});

const instance: BareMetalInstance = {
  $typeName: 'osac.public.v1.BareMetalInstance',
  id: 'bmi-1',
  metadata: {
    $typeName: 'osac.public.v1.Metadata',
    name: 'worker-01',
    displayName: '',
    description: '',
    annotations: {},
    labels: {},
    creator: 'admin',
    project: 'default',
    tenant: 'tenant-1',
    version: 1,
  },
  spec: {
    $typeName: 'osac.public.v1.BareMetalInstanceSpec',
    diskImage: {
      $typeName: 'osac.public.v1.DiskImageReference',
      id: 'disk-1',
      name: 'RHEL 9.4',
      project: 'default',
      shared: false,
    },
    templateParameters: {},
    networkAttachments: [],
    restartTrigger: 0n,
  },
  status: {
    $typeName: 'osac.public.v1.BareMetalInstanceStatus',
    state: 0,
    conditions: [],
    restartTrigger: 0n,
    networkAttachmentStatuses: [],
  },
};

describe('BareMetalInstanceResources', () => {
  it('renders hardware and OS image details from the instance type', () => {
    renderWithProviders(
      <BareMetalInstanceResources instance={instance} instanceType={instanceType} />,
    );

    expect(screen.getByText('CPU')).toBeInTheDocument();
    expect(screen.getByText('64 vCPU')).toBeInTheDocument();
    expect(screen.getByText('512 GB')).toBeInTheDocument();
    expect(screen.getByText('NVIDIA A100 80 GB')).toBeInTheDocument();
    expect(screen.getByText('RHEL 9.4')).toBeInTheDocument();
  });
});
