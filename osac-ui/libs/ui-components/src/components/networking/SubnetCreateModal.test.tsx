import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { Subnet, SubnetState, VirtualNetwork, VirtualNetworkState } from '@osac/types';

import { SubnetCreateModal } from './SubnetCreateModal';
import * as networkingApi from '../../api/v1/networking';

vi.mock('../../api/v1/networking', async (importOriginal) => {
  const actual = await importOriginal<typeof networkingApi>();
  return {
    ...actual,
    useCreateSubnet: vi.fn(),
  };
});

describe('SubnetCreateModal', () => {
  const mockOnClose = vi.fn();
  const mutateAsync = vi.fn();
  const mockParentVN: VirtualNetwork = {
    $typeName: 'osac.public.v1.VirtualNetwork',
    id: 'vn-123',
    metadata: {
      $typeName: 'osac.public.v1.Metadata',
      displayName: '',
      description: '',
      name: 'prod-vn',
      annotations: {},
      creator: 'foo',
      labels: {},
      project: 'foo',
      tenant: 'foo',
      version: 1,
    },
    spec: {
      $typeName: 'osac.public.v1.VirtualNetworkSpec',
      ipv4Cidr: '10.0.0.0/16',
    },
    status: {
      $typeName: 'osac.public.v1.VirtualNetworkStatus',
      state: VirtualNetworkState.READY,
    },
  };
  const mockExistingSubnets: Subnet[] = [
    {
      $typeName: 'osac.public.v1.Subnet',
      id: 'subnet-1',
      metadata: {
        $typeName: 'osac.public.v1.Metadata',
        displayName: '',
        description: '',
        name: 'subnet-web',
        annotations: {},
        creator: 'foo',
        labels: {},
        project: 'foo',
        tenant: 'foo',
        version: 1,
      },
      spec: {
        $typeName: 'osac.public.v1.SubnetSpec',
        virtualNetwork: {
          $typeName: 'osac.public.v1.VirtualNetworkLocalReference',
          id: 'vn-123',
          name: 'prod-vn',
        },
        ipv4Cidr: '10.0.1.0/24',
      },
      status: {
        $typeName: 'osac.public.v1.SubnetStatus',
        state: SubnetState.READY,
      },
    },
  ];

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(networkingApi.useCreateSubnet).mockReturnValue({
      mutateAsync,
      error: null,
    } as unknown as ReturnType<typeof networkingApi.useCreateSubnet>);
  });

  it('renders with Name and CIDR fields', () => {
    render(
      <SubnetCreateModal
        onClose={mockOnClose}
        parentVN={mockParentVN}
        existingSubnets={mockExistingSubnets}
      />,
    );

    expect(screen.getByLabelText(/Name/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/CIDR/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Create/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Cancel/i })).toBeInTheDocument();
  });

  it('displays parent VN CIDR as context', () => {
    render(
      <SubnetCreateModal
        onClose={mockOnClose}
        parentVN={mockParentVN}
        existingSubnets={mockExistingSubnets}
      />,
    );

    expect(screen.getByText(/10\.0\.0\.0\/16/)).toBeInTheDocument();
  });

  it('Create button stays enabled', () => {
    render(
      <SubnetCreateModal
        onClose={mockOnClose}
        parentVN={mockParentVN}
        existingSubnets={mockExistingSubnets}
      />,
    );

    const createButton = screen.getByRole('button', { name: /Create/i });
    expect(createButton).not.toBeDisabled();
  });
});
