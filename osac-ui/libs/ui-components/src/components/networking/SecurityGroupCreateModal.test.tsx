import { MemoryRouter } from 'react-router-dom';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useField } from 'formik';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { VirtualNetworkState } from '@osac/types';

import { SecurityGroupCreateModal } from './SecurityGroupCreateModal';
import * as networkingApi from '../../api/v1/networking';

vi.mock('../../api/v1/networking', async (importOriginal) => {
  const actual = await importOriginal<typeof networkingApi>();
  return {
    ...actual,
    useVirtualNetworks: vi.fn(),
    useCreateSecurityGroup: vi.fn(),
  };
});

vi.mock('../../components/Form/ProjectField', () => ({
  default: function ProjectFieldMock() {
    const [field] = useField('metadata.project');
    return <input aria-label="Project" {...field} />;
  },
}));

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
    vi.mocked(networkingApi.useVirtualNetworks).mockReturnValue({
      data: mockVirtualNetworks,
      isLoading: false,
      error: null,
    } as ReturnType<typeof networkingApi.useVirtualNetworks>);

    vi.mocked(networkingApi.useCreateSecurityGroup).mockReturnValue({
      mutateAsync,
      error: null,
    } as unknown as ReturnType<typeof networkingApi.useCreateSecurityGroup>);
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
    expect(screen.getByRole('button', { name: /Next/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Cancel/i })).toBeInTheDocument();
  });

  it('calls createSecurityGroup on successful submit', async () => {
    const user = userEvent.setup();
    mutateAsync.mockResolvedValue({ id: 'sg-new' });

    renderModal();

    await user.click(screen.getByLabelText(/Virtual Network/i));
    await user.click(screen.getByRole('option', { name: /vn-prod/i }));
    await user.type(screen.getByLabelText('Project'), 'project-a');
    await user.type(screen.getByLabelText(/Name/i), 'sg-web');
    await user.click(screen.getByRole('button', { name: /Next/i }));
    await user.click(screen.getByRole('button', { name: /Create/i }));

    await waitFor(() => {
      expect(mutateAsync).toHaveBeenCalledWith(
        expect.objectContaining({
          metadata: { name: 'sg-web', project: 'project-a', description: '' },
          spec: { virtualNetwork: { id: 'vn-1' }, ingress: [], egress: [] },
        }),
      );
    });
  });

  it('shows error alert when create fails', async () => {
    const user = userEvent.setup();
    mutateAsync.mockRejectedValue(new Error('API error'));
    vi.mocked(networkingApi.useCreateSecurityGroup).mockReturnValue({
      mutateAsync,
      error: new Error('API error'),
    } as unknown as ReturnType<typeof networkingApi.useCreateSecurityGroup>);

    renderModal();

    await user.click(screen.getByLabelText(/Virtual Network/i));
    await user.click(screen.getByRole('option', { name: /vn-prod/i }));
    await user.type(screen.getByLabelText('Project'), 'project-a');
    await user.type(screen.getByLabelText(/Name/i), 'sg-web');
    await user.click(screen.getByRole('button', { name: /Next/i }));
    await user.click(screen.getByRole('button', { name: /Create/i }));

    await waitFor(() => {
      expect(screen.getByText(/API error/i)).toBeInTheDocument();
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
