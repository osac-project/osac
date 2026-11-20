import { type Transport, createRouterTransport } from '@connectrpc/connect';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { SubnetsCreateRequest } from '@osac/types';
import { Subnet, SubnetState, Subnets, VirtualNetwork, VirtualNetworkState } from '@osac/types';

import { SubnetCreateModal } from './SubnetCreateModal';
import * as networkingApi from '../../api/v1/networking';
import { renderWithProviders } from '../../test-utils/TestProviders';

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
        computeInstanceUnsupportedReason: '',
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

  it('submits a trimmed IPv4 CIDR', async () => {
    const user = userEvent.setup();
    mutateAsync.mockResolvedValue({ id: 'subnet-new' });

    render(
      <SubnetCreateModal
        onClose={mockOnClose}
        parentVN={mockParentVN}
        existingSubnets={mockExistingSubnets}
      />,
    );

    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'subnet-new');
    const cidrInput = screen.getByRole('textbox', { name: 'IPv4 CIDR' });
    await user.type(cidrInput, ' 10.0.2.0/24 ');
    expect(cidrInput).toHaveValue(' 10.0.2.0/24 ');

    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => {
      expect(mutateAsync).toHaveBeenCalledWith({
        metadata: { name: 'subnet-new', project: 'foo' },
        spec: { virtualNetwork: { name: 'prod-vn' }, ipv4Cidr: '10.0.2.0/24' },
      });
    });
  });

  it('creates through the Subnets service without sending an Update', async () => {
    const actual = await vi.importActual<typeof networkingApi>('../../api/v1/networking');
    vi.mocked(networkingApi.useCreateSubnet).mockImplementation(actual.useCreateSubnet);
    let createRequest: SubnetsCreateRequest | undefined;
    const methods: string[] = [];
    const inner = createRouterTransport((router) => {
      router.service(Subnets, {
        create: (req) => {
          createRequest = req;
          return { object: { ...req.object, id: 'subnet-real' } };
        },
      });
    });
    const transport: Transport = {
      ...inner,
      unary(method, ...args) {
        methods.push(method.name);
        return inner.unary(method, ...args);
      },
    };
    const { user } = renderWithProviders(
      <SubnetCreateModal
        onClose={mockOnClose}
        parentVN={mockParentVN}
        existingSubnets={mockExistingSubnets}
      />,
      { transport },
    );

    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'subnet-real');
    await user.type(screen.getByRole('textbox', { name: 'IPv4 CIDR' }), '10.0.3.0/24');
    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(createRequest).toBeDefined());
    expect(createRequest?.object?.metadata?.name).toBe('subnet-real');
    expect(createRequest?.object?.metadata?.project).toBe('foo');
    expect(createRequest?.object?.spec?.virtualNetwork?.name).toBe('prod-vn');
    expect(createRequest?.object?.spec?.ipv4Cidr).toBe('10.0.3.0/24');
    expect(methods).toEqual(['Create']);
    expect(methods).not.toContain('Update');
    await waitFor(() => expect(mockOnClose).toHaveBeenCalled());
  });
});
