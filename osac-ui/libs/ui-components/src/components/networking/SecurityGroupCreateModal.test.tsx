import { MemoryRouter } from 'react-router-dom';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { VirtualNetworkState } from '@osac/types';

import { SecurityGroupCreateModal } from './SecurityGroupCreateModal';
import * as resourceApi from '../../api/use-resource';

vi.mock('../../api/use-resource', async (importOriginal) => {
  const actual = await importOriginal<typeof resourceApi>();
  return {
    ...actual,
    useListResource: vi.fn(),
    useCreateResource: vi.fn(),
  };
});

describe('SecurityGroupCreateModal', () => {
  const mockVirtualNetworks = [
    {
      id: 'vn-1',
      metadata: { name: 'vn-prod' },
      spec: { ipv4Cidr: '10.0.0.0/16' },
      status: { state: VirtualNetworkState.READY },
    },
    {
      id: 'vn-2',
      metadata: { name: 'vn-dev' },
      spec: { ipv4Cidr: '192.168.0.0/16' },
      status: { state: VirtualNetworkState.READY },
    },
  ];

  const mutateAsync = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(resourceApi.useListResource).mockReturnValue({
      data: {
        items: mockVirtualNetworks,
        size: mockVirtualNetworks.length,
        total: mockVirtualNetworks.length,
      },
      isLoading: false,
      error: null,
    } as unknown as ReturnType<typeof resourceApi.useListResource>);

    vi.mocked(resourceApi.useCreateResource).mockReturnValue({
      mutateAsync,
      error: null,
    } as unknown as ReturnType<typeof resourceApi.useCreateResource>);
  });

  const renderModal = (onClose = vi.fn()) =>
    render(
      <MemoryRouter>
        <SecurityGroupCreateModal onClose={onClose} />
      </MemoryRouter>,
    );

  it('renders modal with VN dropdown and Name field', () => {
    renderModal();

    expect(screen.getByText('Create security group')).toBeInTheDocument();
    expect(screen.getByLabelText(/Virtual Network/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/Name/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Create/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Cancel/i })).toBeInTheDocument();
  });

  it('calls createSecurityGroup on successful submit', async () => {
    const user = userEvent.setup();
    mutateAsync.mockResolvedValue({ object: { id: 'sg-new' } });

    renderModal();

    await user.click(screen.getByLabelText(/Virtual Network/i));
    await user.click(screen.getByRole('option', { name: /vn-prod/i }));
    await user.type(screen.getByLabelText(/Name/i), 'sg-web');
    await user.click(screen.getByRole('button', { name: /Create/i }));

    await waitFor(() => {
      expect(mutateAsync).toHaveBeenCalledWith(
        expect.objectContaining({
          object: {
            metadata: { name: 'sg-web' },
            spec: { virtualNetwork: { id: 'vn-1' }, ingress: [], egress: [] },
          },
        }),
      );
    });
  });

  it('shows error alert when create fails', async () => {
    const user = userEvent.setup();
    mutateAsync.mockRejectedValue(new Error('API error'));
    vi.mocked(resourceApi.useCreateResource).mockReturnValue({
      mutateAsync,
      error: new Error('API error'),
    } as unknown as ReturnType<typeof resourceApi.useCreateResource>);

    renderModal();

    await user.click(screen.getByLabelText(/Virtual Network/i));
    await user.click(screen.getByRole('option', { name: /vn-prod/i }));
    await user.type(screen.getByLabelText(/Name/i), 'sg-web');
    await user.click(screen.getByRole('button', { name: /Create/i }));

    await waitFor(() => {
      expect(screen.getByText(/API error/i)).toBeInTheDocument();
    });
  });

  it('shows error alert when create response is missing an id', async () => {
    const user = userEvent.setup();
    mutateAsync.mockResolvedValue({ object: {} });

    renderModal();

    await user.click(screen.getByLabelText(/Virtual Network/i));
    await user.click(screen.getByRole('option', { name: /vn-prod/i }));
    await user.type(screen.getByLabelText(/Name/i), 'sg-web');
    await user.click(screen.getByRole('button', { name: /Create/i }));

    await waitFor(() => {
      expect(screen.getByText('Create response missing id')).toBeInTheDocument();
    });
  });

  it('calls onClose when Cancel is clicked', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();

    renderModal(onClose);

    await user.click(screen.getByRole('button', { name: /Cancel/i }));
    expect(onClose).toHaveBeenCalled();
  });
});
