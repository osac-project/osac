import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type {
  Subnet,
  SubnetsCreateRequest,
  SubnetsCreateResponse,
  VirtualNetwork,
} from '@osac/types';
import { SubnetState, VirtualNetworkState } from '@osac/types';

import { SubnetCreateModal } from './SubnetCreateModal';
import type { UserRole } from '../../shellTypes';
import { renderWithProviders } from '../../test-utils/TestProviders';

const readyVn = {
  id: 'vn-123',
  metadata: { name: 'prod-vn', project: 'proj-a', tenant: 'tenant-a' },
  spec: { ipv4Cidr: '10.0.0.0/16' },
  status: { state: VirtualNetworkState.READY },
} as VirtualNetwork;

const pendingVn = {
  id: 'vn-pending',
  metadata: { name: 'pending-vn', project: 'proj-a' },
  spec: { ipv4Cidr: '10.9.0.0/16' },
  status: { state: VirtualNetworkState.PENDING },
} as VirtualNetwork;

const existingSubnet = {
  id: 'subnet-1',
  metadata: { name: 'subnet-web' },
  spec: { virtualNetwork: { id: 'vn-123' }, ipv4Cidr: '10.0.1.0/24' },
  status: { state: SubnetState.READY },
} as Subnet;

const renderModal = ({
  virtualNetworkId,
  lockVirtualNetwork,
  role = 'tenant-admin',
  onCreate,
}: {
  virtualNetworkId?: string;
  lockVirtualNetwork?: boolean;
  role?: UserRole;
  onCreate?: (req: SubnetsCreateRequest) => void;
} = {}) => {
  const onClose = vi.fn();
  const view = renderWithProviders(
    <SubnetCreateModal
      onClose={onClose}
      virtualNetworkId={virtualNetworkId}
      lockVirtualNetwork={lockVirtualNetwork}
    />,
    {
      apiFixtures: { virtualNetworks: [readyVn, pendingVn], subnets: [existingSubnet] },
      transportOverrides: {
        onSubnetCreate: (req) => {
          onCreate?.(req);
          return { object: { ...req.object, id: 'subnet-new' } } as SubnetsCreateResponse;
        },
      },
      session: { role },
    },
  );
  return { ...view, onClose };
};

const fillForm = async (user: ReturnType<typeof renderModal>['user'], cidr: string) => {
  await user.type(screen.getByLabelText(/^Name/), 'subnet-app');
  await user.type(screen.getByLabelText(/^IPv4 CIDR/), cidr);
};

describe('SubnetCreateModal', () => {
  it('offers only ready virtual networks', async () => {
    const { user } = renderModal();

    const picker = await screen.findByLabelText(/^Virtual network/);
    await waitFor(() => expect(picker).toBeEnabled());
    await user.click(picker);

    expect(screen.getByRole('option', { name: /prod-vn/ })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: /pending-vn/ })).not.toBeInTheDocument();
  });

  it('tells apart same-named networks by tenant for a provider admin', async () => {
    const otherDefault = {
      id: 'vn-b',
      metadata: { name: 'prod-vn', project: '', tenant: 'tenant-b' },
      spec: { ipv4Cidr: '10.0.0.0/16' },
      status: { state: VirtualNetworkState.READY },
    } as VirtualNetwork;
    const { user } = renderWithProviders(<SubnetCreateModal onClose={vi.fn()} />, {
      apiFixtures: { virtualNetworks: [readyVn, otherDefault], subnets: [] },
      session: { role: 'admin', tenantId: '' },
    });

    const picker = await screen.findByLabelText(/^Virtual network/);
    await waitFor(() => expect(picker).toBeEnabled());
    await user.click(picker);

    expect(
      screen.getByRole('option', { name: 'prod-vn (10.0.0.0/16, tenant-a)' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('option', { name: 'prod-vn (10.0.0.0/16, tenant-b)' }),
    ).toBeInTheDocument();
  });

  it('pre-fills the virtual network and shows its CIDR in the hints', async () => {
    renderModal({ virtualNetworkId: 'vn-123' });

    expect(await screen.findByText('Must be inside 10.0.0.0/16.')).toBeInTheDocument();
    expect(screen.getByLabelText(/^Virtual network/)).toHaveTextContent('prod-vn');
    expect(screen.getByLabelText(/^Virtual network/)).toBeEnabled();
    // The example lies inside the network, never the whole network.
    expect(screen.getByLabelText(/^IPv4 CIDR/)).toHaveAttribute('placeholder', '10.0.0.0/24');
    expect(screen.getByText('Only ready virtual networks are listed.')).toBeInTheDocument();
    expect(screen.queryByText(/Parent virtual network CIDR/)).not.toBeInTheDocument();
  });

  it('suggests a CIDR example that no subnet of the network uses', async () => {
    renderWithProviders(<SubnetCreateModal onClose={vi.fn()} virtualNetworkId="vn-123" />, {
      apiFixtures: {
        virtualNetworks: [readyVn],
        subnets: [
          {
            ...existingSubnet,
            id: 'subnet-0',
            spec: { virtualNetwork: { id: 'vn-123' }, ipv4Cidr: '10.0.0.0/24' },
          } as Subnet,
        ],
      },
      session: { role: 'tenant-admin' },
    });

    await screen.findByText('Must be inside 10.0.0.0/16.');
    await waitFor(() =>
      expect(screen.getByLabelText(/^IPv4 CIDR/)).toHaveAttribute('placeholder', '10.0.1.0/24'),
    );
  });

  it('does not pre-fill a virtual network from the filter that is not ready', async () => {
    const onCreate = vi.fn();
    const { user } = renderModal({ virtualNetworkId: 'vn-pending', onCreate });

    const picker = await screen.findByLabelText(/^Virtual network/);
    await waitFor(() => expect(picker).toBeEnabled());
    expect(picker).toHaveTextContent('Select a virtual network');
    expect(screen.queryByText('10.9.0.0/16')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Create' }));

    expect(await screen.findByText('Virtual network is required')).toBeInTheDocument();
    expect(onCreate).not.toHaveBeenCalled();
  });

  it('locks the virtual network when asked to', async () => {
    renderModal({ virtualNetworkId: 'vn-123', lockVirtualNetwork: true });

    expect(await screen.findByText('Must be inside 10.0.0.0/16.')).toBeInTheDocument();
    expect(screen.getByLabelText(/^Virtual network/)).toBeDisabled();
    // A locked picker needs no hint about which networks it lists.
    expect(screen.queryByText('Only ready virtual networks are listed.')).not.toBeInTheDocument();
  });

  it('sends the virtual network by id with its project', async () => {
    const onCreate = vi.fn();
    const { user, onClose } = renderModal({ virtualNetworkId: 'vn-123', onCreate });
    await screen.findByText('Must be inside 10.0.0.0/16.');

    await fillForm(user, '10.0.2.0/24');
    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    const request = onCreate.mock.calls[0][0] as SubnetsCreateRequest;
    expect(request.object?.spec?.virtualNetwork?.id).toBe('vn-123');
    expect(request.object?.spec?.virtualNetwork?.name).toBe('');
    expect(request.object?.spec?.ipv4Cidr).toBe('10.0.2.0/24');
    expect(request.object?.metadata?.project).toBe('proj-a');
    expect(request.object?.metadata?.tenant).toBe('');
  });

  it('submits a trimmed IPv4 CIDR', async () => {
    const onCreate = vi.fn();
    const { user, onClose } = renderModal({ virtualNetworkId: 'vn-123', onCreate });
    await screen.findByText('Must be inside 10.0.0.0/16.');

    await fillForm(user, ' 10.0.2.0/24 ');
    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect((onCreate.mock.calls[0][0] as SubnetsCreateRequest).object?.spec?.ipv4Cidr).toBe(
      '10.0.2.0/24',
    );
  });

  it("creates in the virtual network's tenant for a provider admin", async () => {
    const onCreate = vi.fn();
    const { user, onClose } = renderModal({ virtualNetworkId: 'vn-123', onCreate, role: 'admin' });
    await screen.findByText('Must be inside 10.0.0.0/16.');

    await fillForm(user, '10.0.2.0/24');
    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect((onCreate.mock.calls[0][0] as SubnetsCreateRequest).object?.metadata?.tenant).toBe(
      'tenant-a',
    );
  });

  it('rejects a CIDR outside the virtual network or overlapping a subnet', async () => {
    const onCreate = vi.fn();
    const { user } = renderModal({ virtualNetworkId: 'vn-123', onCreate });
    await screen.findByText('Must be inside 10.0.0.0/16.');

    await fillForm(user, '192.168.0.0/24');
    await user.click(screen.getByRole('button', { name: 'Create' }));
    expect(
      await screen.findByText('CIDR must be within parent virtual network range'),
    ).toBeInTheDocument();

    const cidr = screen.getByLabelText(/^IPv4 CIDR/);
    await user.clear(cidr);
    await user.type(cidr, '10.0.1.0/25');
    await user.tab();
    expect(
      await screen.findByText('CIDR overlaps with existing subnet "subnet-web" (10.0.1.0/24)'),
    ).toBeInTheDocument();
    expect(onCreate).not.toHaveBeenCalled();
  });

  it('shows the server error in the modal', async () => {
    const { user } = renderWithProviders(
      <SubnetCreateModal onClose={vi.fn()} virtualNetworkId="vn-123" />,
      {
        apiFixtures: { virtualNetworks: [readyVn], subnets: [] },
        transportOverrides: {
          onSubnetCreate: () => {
            throw new ConnectError('prefix too long for the address plan', Code.InvalidArgument);
          },
        },
        session: { role: 'tenant-admin' },
      },
    );
    await screen.findByText('Must be inside 10.0.0.0/16.');

    await fillForm(user, '10.0.2.0/24');
    await user.click(screen.getByRole('button', { name: 'Create' }));

    const dialog = within(screen.getByRole('dialog'));
    expect(await dialog.findByText('Failed to create subnet')).toBeInTheDocument();
    expect(dialog.getByText(/prefix too long/)).toBeInTheDocument();
  });

  describe('in a tenant default network', () => {
    const defaultVn = {
      id: 'vn-default',
      metadata: { name: 'default', labels: { 'osac.openshift.io/default': 'true' } },
      spec: { ipv4Cidr: '192.168.0.0/16' },
      status: { state: VirtualNetworkState.READY },
    } as unknown as VirtualNetwork;
    const defaultSubnet = (state: SubnetState) =>
      ({
        id: 'subnet-default',
        metadata: { name: 'default-ipv4', labels: { 'osac.openshift.io/default': 'true' } },
        spec: { virtualNetwork: { id: 'vn-default' }, ipv4Cidr: '192.168.0.0/24' },
        status: { state },
      }) as unknown as Subnet;
    const render = (state: SubnetState) =>
      renderWithProviders(
        <SubnetCreateModal onClose={vi.fn()} virtualNetworkId="vn-default" lockVirtualNetwork />,
        { apiFixtures: { virtualNetworks: [defaultVn], subnets: [defaultSubnet(state)] } },
      );

    it('disables Create until the default subnet is Ready', async () => {
      const { user } = render(SubnetState.PENDING);

      await waitFor(() =>
        expect(screen.getByRole('button', { name: 'Create' })).toHaveAttribute(
          'aria-disabled',
          'true',
        ),
      );
      const create = screen.getByRole('button', { name: 'Create' });
      expect(screen.getByText('Available when the default subnet is Ready.')).toBeInTheDocument();
      await user.hover(create);
      expect(await screen.findByRole('tooltip')).toHaveTextContent(
        'Available when the default subnet is Ready.',
      );
    });

    it('enables Create once the default subnet is Ready', async () => {
      render(SubnetState.READY);

      const create = await screen.findByRole('button', { name: 'Create' });
      await waitFor(() =>
        expect(
          screen.queryByText('Available when the default subnet is Ready.'),
        ).not.toBeInTheDocument(),
      );
      expect(create).not.toHaveAttribute('aria-disabled', 'true');
    });

    it('leaves Create to the server when the subnets fail to load', async () => {
      const onSubnetList = vi.fn(() => {
        throw new ConnectError('unavailable', Code.Unavailable);
      });
      renderWithProviders(
        <SubnetCreateModal onClose={vi.fn()} virtualNetworkId="vn-default" lockVirtualNetwork />,
        {
          apiFixtures: { virtualNetworks: [defaultVn], subnets: [] },
          transportOverrides: { onSubnetList },
        },
      );

      await waitFor(() => expect(onSubnetList).toHaveBeenCalled());
      const create = await screen.findByRole('button', { name: 'Create' });
      await waitFor(() => expect(create).not.toHaveAttribute('aria-disabled', 'true'));
      expect(
        screen.queryByText('Available when the default subnet is Ready.'),
      ).not.toBeInTheDocument();
    });
  });
});
