import { create } from '@bufbuild/protobuf';
import { createRouterTransport } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { type Project, Projects, VirtualNetworkSchema } from '@osac/types';
import { type Tenant, TenantState, Tenants } from '@osac/types/private';

import { VirtualNetworkCreateModal } from './VirtualNetworkCreateModal';
import * as networkingApi from '../../api/v1/networking';
import { renderWithProviders } from '../../test-utils/TestProviders';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

vi.mock('../../api/v1/networking', async (importOriginal) => {
  const actual = await importOriginal<typeof networkingApi>();
  return {
    ...actual,
    useCreateVirtualNetwork: vi.fn(),
  };
});

const projects: Project[] = [
  {
    $typeName: 'osac.public.v1.Project',
    id: 'project-1',
    metadata: {
      $typeName: 'osac.public.v1.Metadata',
      name: 'my-project',
      annotations: {},
      creator: 'foo',
      labels: {},
      project: '',
      tenant: 'foo',
      version: 1,
    },
    spec: {
      $typeName: 'osac.public.v1.ProjectSpec',
      title: 'My Project',
    },
  } as Project,
];

const makeTenant = (id: string, name: string): Tenant =>
  ({
    id,
    metadata: { name },
    spec: { domains: [`${name}.example.com`] },
    status: { state: TenantState.SYNCED },
  }) as Tenant;

interface RenderOptions {
  role?: 'admin' | 'tenant-admin' | 'tenant-user' | 'tenant-idp-manager';
  tenants?: Tenant[];
}

const renderModal = (onClose: () => void, options: RenderOptions = {}) =>
  renderWithProviders(<VirtualNetworkCreateModal onClose={onClose} />, {
    apiFixtures: { projects, tenants: options.tenants },
    session: { role: options.role ?? 'tenant-admin' },
  });

describe('VirtualNetworkCreateModal', () => {
  const mockOnClose = vi.fn();
  const mutateAsync = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(networkingApi.useCreateVirtualNetwork).mockReturnValue({
      mutateAsync,
      error: null,
    } as unknown as ReturnType<typeof networkingApi.useCreateVirtualNetwork>);
  });

  it('renders with Name and IPv4 CIDR fields', () => {
    renderModal(mockOnClose);

    expect(screen.getByLabelText(/Name/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/IPv4 CIDR/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Create/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Cancel/i })).toBeInTheDocument();
  });

  it('Create button stays enabled', () => {
    renderModal(mockOnClose);

    const createButton = screen.getByRole('button', { name: /Create/i });
    expect(createButton).not.toBeDisabled();
  });

  it('does not show a tenant selector for a tenant admin', () => {
    renderModal(mockOnClose);

    expect(screen.queryByLabelText(/^Tenant/)).not.toBeInTheDocument();
  });

  it('keeps the project field idle and disabled before a tenant is selected', async () => {
    renderModal(mockOnClose, {
      role: 'admin',
      tenants: [makeTenant('tenant-1', 'tenant-1')],
    });

    const project = screen.getByLabelText(/^Project/);
    await waitFor(() => {
      expect(project).toBeDisabled();
    });
    expect(project).toHaveTextContent('Root');
    expect(screen.queryByText('Loading projects...')).not.toBeInTheDocument();
  });

  it('requires a tenant selection for a cloud provider admin', async () => {
    const { user } = renderModal(mockOnClose, {
      role: 'admin',
      tenants: [makeTenant('tenant-1', 'tenant-1')],
    });

    expect(await screen.findByLabelText(/^Tenant/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^Project/)).toBeDisabled();

    await user.type(screen.getByLabelText(/Name/i), 'vn-prod');
    await user.type(screen.getByLabelText(/IPv4 CIDR/i), '192.168.10.0/24');
    await user.click(screen.getByRole('button', { name: /Create/i }));

    await waitFor(() => {
      expect(screen.getByText('Tenant is required')).toBeInTheDocument();
    });
    expect(mutateAsync).not.toHaveBeenCalled();
  });

  it('creates the virtual network in the selected tenant for a cloud provider admin', async () => {
    mutateAsync.mockResolvedValue({ id: 'vn-new' });

    const { user } = renderModal(mockOnClose, {
      role: 'admin',
      tenants: [makeTenant('tenant-1', 'tenant-1')],
    });

    await waitFor(() => {
      expect(screen.getByLabelText(/^Tenant/)).not.toBeDisabled();
    });
    await user.click(screen.getByLabelText(/^Tenant/));
    await user.click(screen.getByRole('option', { name: 'tenant-1' }));
    await user.type(screen.getByLabelText(/Name/i), 'vn-prod');
    await user.type(screen.getByLabelText(/IPv4 CIDR/i), '192.168.10.0/24');
    await user.click(screen.getByRole('button', { name: /Create/i }));

    await waitFor(() => {
      expect(mutateAsync).toHaveBeenCalledWith(
        expect.objectContaining({
          metadata: { name: 'vn-prod', project: '', tenant: 'tenant-1' },
          // eslint-disable-next-line @typescript-eslint/no-unsafe-assignment
          spec: expect.objectContaining({
            ipv4Cidr: '192.168.10.0/24',
          }),
        }),
      );
    });
  });

  it("resets the project and offers only the new tenant's projects after a tenant switch", async () => {
    mutateAsync.mockResolvedValue({ id: 'vn-new' });
    let releaseTenantTwo = () => undefined as void;
    const tenantTwoReady = new Promise<void>((resolve) => {
      releaseTenantTwo = resolve;
    });
    const tenantProject = (id: string, name: string, tenant: string) =>
      ({ id, metadata: { name, project: '', tenant }, spec: { title: name } }) as Project;
    const transport = createRouterTransport((router) => {
      router.service(Tenants, {
        list: () => {
          const items = [makeTenant('tenant-1', 'tenant-1'), makeTenant('tenant-2', 'tenant-2')];
          return { items, size: items.length, total: items.length };
        },
      });
      router.service(Projects, {
        list: async (req) => {
          if (req.filter?.includes('"tenant-2"')) {
            await tenantTwoReady;
            return { items: [tenantProject('p-2', 'team-two', 'tenant-2')], size: 1, total: 1 };
          }
          return { items: [tenantProject('p-1', 'team-one', 'tenant-1')], size: 1, total: 1 };
        },
      });
    });
    const { user } = renderWithProviders(<VirtualNetworkCreateModal onClose={mockOnClose} />, {
      transport,
      session: { role: 'admin' },
    });

    await waitFor(() => {
      expect(screen.getByLabelText(/^Tenant/)).not.toBeDisabled();
    });
    await user.click(screen.getByLabelText(/^Tenant/));
    await user.click(screen.getByRole('option', { name: 'tenant-1' }));
    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Project' })).not.toBeDisabled();
    });
    await user.click(screen.getByRole('button', { name: 'Project' }));
    await user.click(await screen.findByRole('option', { name: 'team-one' }));
    expect(screen.getByRole('button', { name: 'Project' })).toHaveTextContent('team-one');

    await user.click(screen.getByLabelText(/^Tenant/));
    await user.click(screen.getByRole('option', { name: 'tenant-2' }));
    expect(await screen.findByText('Loading projects...')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Project' })).not.toHaveTextContent('team-one');
    await user.click(screen.getByRole('button', { name: 'Project' }));
    expect(screen.queryByRole('option', { name: 'team-one' })).not.toBeInTheDocument();
    await user.keyboard('{Escape}');

    releaseTenantTwo();
    await waitFor(() => {
      expect(screen.queryByText('Loading projects...')).not.toBeInTheDocument();
    });
    await user.click(screen.getByRole('button', { name: 'Project' }));
    expect(await screen.findByRole('option', { name: 'team-two' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'team-one' })).not.toBeInTheDocument();
    await user.keyboard('{Escape}');

    await user.type(screen.getByLabelText(/Name/i), 'vn-prod');
    await user.type(screen.getByLabelText(/IPv4 CIDR/i), '192.168.10.0/24');
    await user.click(screen.getByRole('button', { name: /Create/i }));

    await waitFor(() => {
      expect(mutateAsync).toHaveBeenCalledWith(
        expect.objectContaining({
          metadata: { name: 'vn-prod', project: '', tenant: 'tenant-2' },
        }),
      );
    });
  });

  it('shows validation errors and does not submit when Name and CIDRs are empty', async () => {
    const { user } = renderModal(mockOnClose);

    await user.click(screen.getByRole('button', { name: /Create/i }));

    await waitFor(() => {
      expect(screen.getByText('Name is required')).toBeInTheDocument();
    });
    expect(mutateAsync).not.toHaveBeenCalled();
  });

  it('calls create and navigates on successful submit', async () => {
    mutateAsync.mockResolvedValue({ id: 'vn-new' });

    const { user } = renderModal(mockOnClose);

    await user.click(screen.getByLabelText(/^Project/));
    await user.click(screen.getByRole('option', { name: 'My Project' }));
    await user.type(screen.getByLabelText(/Name/i), 'vn-prod');
    await user.type(screen.getByLabelText(/IPv4 CIDR/i), '10.0.0.0/16');
    await user.click(screen.getByRole('button', { name: /Create/i }));

    await waitFor(() => {
      expect(mutateAsync).toHaveBeenCalledWith(
        expect.objectContaining({
          metadata: { name: 'vn-prod', project: 'my-project' },
          // eslint-disable-next-line @typescript-eslint/no-unsafe-assignment
          spec: expect.objectContaining({
            ipv4Cidr: '10.0.0.0/16',
          }),
        }),
      );
      expect(mockNavigate).toHaveBeenCalledWith('/networking/virtual-networks/vn-new');
    });
  });

  it('returns a created network to the wizard with its selected project', async () => {
    const onCreated = vi.fn();
    const network = create(VirtualNetworkSchema, {
      id: 'vn-new',
      metadata: { name: 'vn-prod' },
    });
    mutateAsync.mockResolvedValue(network);

    const { user } = renderWithProviders(
      <VirtualNetworkCreateModal
        onClose={mockOnClose}
        onCreated={onCreated}
        initialProject="my-project"
      />,
      { apiFixtures: { projects }, session: { role: 'tenant-admin' } },
    );

    await user.type(screen.getByLabelText(/Name/i), 'vn-prod');
    await user.type(screen.getByLabelText(/IPv4 CIDR/i), '10.0.0.0/16');
    await user.click(screen.getByRole('button', { name: /Create/i }));

    await waitFor(() => {
      expect(mutateAsync).toHaveBeenCalledWith(
        expect.objectContaining({ metadata: { name: 'vn-prod', project: 'my-project' } }),
      );
      expect(onCreated).toHaveBeenCalledWith(network);
    });
    expect(mockNavigate).not.toHaveBeenCalled();
  });

  it('shows error alert when create fails', async () => {
    mutateAsync.mockRejectedValue(new Error('API error'));
    vi.mocked(networkingApi.useCreateVirtualNetwork).mockReturnValue({
      mutateAsync,
      error: new Error('API error'),
    } as unknown as ReturnType<typeof networkingApi.useCreateVirtualNetwork>);

    const { user } = renderModal(mockOnClose);

    await user.type(screen.getByLabelText(/Name/i), 'vn-prod');
    await user.type(screen.getByLabelText(/IPv4 CIDR/i), '10.0.0.0/16');
    await user.click(screen.getByRole('button', { name: /Create/i }));

    await waitFor(() => {
      expect(screen.getByText(/API error/i)).toBeInTheDocument();
    });
  });
});
