import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import {
  type BareMetalInstance,
  type BareMetalInstanceCatalogItem,
  BareMetalInstanceState,
  BareMetalInstanceStatus,
  BareMetalInstanceTypeSchema,
} from '@osac/types';

import BareMetalInstanceCard from './BareMetalInstanceCard';
import { renderWithProviders } from '../../../test-utils/TestProviders';
import { formatBareMetalCreatedAt } from '../bareMetalInstanceDisplay';

vi.mock('@osac/ui-components/hooks/use-session.tsx', () => ({
  useSession: vi.fn(() => ({
    role: 'tenant-user',
    username: 'test-user',
    tenantId: 'test-tenant',
    projects: [],
    setProjects: vi.fn(),
  })),
}));

const gpuLargeInstanceType = create(BareMetalInstanceTypeSchema, {
  id: 'gpu-large',
  metadata: { name: 'gpu-large' },
  spec: {
    description: 'GPU-enabled bare metal host',
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

const instanceStatus: BareMetalInstanceStatus = {
  $typeName: 'osac.public.v1.BareMetalInstanceStatus',
  state: BareMetalInstanceState.RUNNING,
  conditions: [],
  restartTrigger: 0n,
  networkAttachmentStatuses: [
    {
      $typeName: 'osac.public.v1.BareMetalNetworkAttachmentStatus',
      interface: 'eth0',
      subnetRef: 'subnet-1',
      ipAddress: '10.0.0.14',
      primary: true,
    },
  ],
};

const instance: BareMetalInstance = {
  $typeName: 'osac.public.v1.BareMetalInstance',
  id: 'bmi-1',
  metadata: {
    $typeName: 'osac.public.v1.Metadata',
    name: 'bm-server-04',
    displayName: '',
    description: '',
    annotations: {},
    labels: {},
    creator: 'admin',
    project: 'ml-project',
    tenant: 'tenant-1',
    version: 1,
    creationTimestamp: { $typeName: 'google.protobuf.Timestamp', seconds: 1726639380n, nanos: 0 },
  },
  spec: {
    $typeName: 'osac.public.v1.BareMetalInstanceSpec',
    catalogItem: {
      $typeName: 'osac.public.v1.BareMetalInstanceCatalogItemReference',
      id: 'catalog-1',
      name: 'bare-metal-gpu-training-server',
      project: 'default',
      shared: false,
    },
    instanceType: {
      $typeName: 'osac.public.v1.BareMetalInstanceTypeReference',
      id: 'gpu-large',
      name: 'gpu-large',
      project: 'default',
      shared: false,
    },
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
  status: instanceStatus,
};

const sshHostsByInstanceId = new Map([['bmi-1', '10.0.0.14']]);

describe('BareMetalInstanceCard', () => {
  it('renders bare metal instance details in a card', async () => {
    const { user } = renderWithProviders(
      <BareMetalInstanceCard
        instance={instance}
        instanceTypes={[gpuLargeInstanceType]}
        sshHost={sshHostsByInstanceId.get(instance.id)}
      />,
    );

    expect(screen.getByRole('link', { name: 'bm-server-04' })).toHaveAttribute(
      'href',
      '/bare-metal/bmi-1',
    );
    expect(screen.getByText('bare-metal-gpu-training-server')).toBeInTheDocument();
    expect(screen.getByText('Running')).toBeInTheDocument();
    expect(screen.getByText('64 vCPU')).toBeInTheDocument();
    expect(screen.getByText('512 GB')).toBeInTheDocument();
    expect(screen.getByText('NVIDIA A100 80 GB')).toBeInTheDocument();
    expect(screen.getByText('RHEL 9.4')).toBeInTheDocument();
    expect(screen.getByText('ml-project')).toBeInTheDocument();
    expect(screen.getByText(formatBareMetalCreatedAt(instance.metadata))).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Actions for bm-server-04' })).toBeInTheDocument();

    const connectButton = screen.getByRole('button', { name: 'Connect via SSH' });
    expect(connectButton).toBeEnabled();

    await user.click(connectButton);

    expect(screen.getByRole('dialog', { name: 'Connect via SSH' })).toBeInTheDocument();
    expect(screen.getByText('10.0.0.14')).toBeInTheDocument();
    expect(screen.getByDisplayValue('ssh test-user@10.0.0.14')).toBeInTheDocument();
  });

  it('resolves hardware from the catalog item when the instance spec omits instance type', () => {
    const catalogItems: BareMetalInstanceCatalogItem[] = [
      {
        id: 'catalog-1',
        metadata: { name: 'bare-metal-gpu-training-server' },
        fields: {
          instanceType: {
            behavior: {
              case: 'locked',
              value: {
                id: 'gpu-large',
                name: 'gpu-large',
                project: 'default',
                shared: false,
              },
            },
          },
        },
      } as BareMetalInstanceCatalogItem,
    ];
    const instanceFromCatalog = {
      ...instance,
      spec: {
        ...instance.spec,
        instanceType: undefined,
      },
    } as unknown as BareMetalInstance;

    renderWithProviders(
      <BareMetalInstanceCard
        instance={instanceFromCatalog}
        instanceTypes={[gpuLargeInstanceType]}
        catalogItems={catalogItems}
      />,
    );

    expect(screen.getByText('64 vCPU')).toBeInTheDocument();
    expect(screen.getByText('512 GB')).toBeInTheDocument();
    expect(screen.getByText('NVIDIA A100 80 GB')).toBeInTheDocument();
  });

  it('disables Connect via SSH when the instance is not running', () => {
    renderWithProviders(
      <BareMetalInstanceCard
        instance={{
          ...instance,
          status: {
            ...instanceStatus,
            state: BareMetalInstanceState.STOPPED,
          },
        }}
        instanceTypes={[gpuLargeInstanceType]}
      />,
    );

    expect(screen.getByRole('button', { name: 'Connect via SSH' })).toBeDisabled();
  });
});
