import { Route, Routes } from 'react-router-dom';
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { Cluster, ExternalIP, NATGateway, Subnet, VirtualNetwork } from '@osac/types';
import { ExternalIPState, NATGatewayState, VirtualNetworkState } from '@osac/types';

import { NatGatewaysListPage } from './NatGatewaysListPage';
import type { UserRole } from '../../shellTypes';
import { renderWithProviders } from '../../test-utils/TestProviders';

const vn = (id: string, name: string, extra: Partial<VirtualNetwork> = {}) =>
  ({
    id,
    metadata: { name },
    spec: { ipv4Cidr: '10.0.0.0/16' },
    status: { state: VirtualNetworkState.READY },
    ...extra,
  }) as VirtualNetwork;

const virtualNetworks = [
  vn('vn-1', 'vn-prod'),
  vn('vn-free', 'vn-free', { metadata: { name: 'vn-free', tenant: 'tenant-a' } } as never),
  vn('vn-pending', 'vn-pending', { status: { state: VirtualNetworkState.PENDING } } as never),
  vn('vn-default', 'vn-default', {
    metadata: { name: 'vn-default', labels: { 'osac.openshift.io/default': 'true' } },
  } as never),
];

const natGateways = [
  {
    id: 'nat-1',
    metadata: { name: 'nat-egress', tenant: 'tenant-a' },
    spec: { virtualNetwork: { id: 'vn-1' }, externalIp: { id: 'eip-1' } },
    status: { state: NATGatewayState.NAT_GATEWAY_STATE_READY },
  },
  {
    id: 'nat-default',
    metadata: { name: 'default', labels: { 'osac.openshift.io/default': 'true' } },
    spec: { virtualNetwork: { id: 'vn-default' }, externalIp: { id: 'eip-2' } },
    status: { state: NATGatewayState.NAT_GATEWAY_STATE_READY },
  },
] as unknown as NATGateway[];

const externalIps = [
  {
    id: 'eip-1',
    metadata: { name: 'eip-1' },
    status: { state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED, address: '203.0.113.10' },
  },
] as ExternalIP[];

const renderPage = (
  role: UserRole = 'tenant-user',
  onNatGatewayDelete = vi.fn(),
  {
    fixtureNatGateways = natGateways,
    routerEntries = ['/networking/nat-gateways'],
  }: { fixtureNatGateways?: NATGateway[]; routerEntries?: string[] } = {},
) =>
  renderWithProviders(
    <Routes>
      <Route path="/networking/nat-gateways" element={<NatGatewaysListPage />} />
      <Route path="/networking/nat-gateways/create/:id" element={<h1>Attach wizard</h1>} />
    </Routes>,
    {
      apiFixtures: { virtualNetworks, natGateways: fixtureNatGateways, externalIps },
      transportOverrides: {
        onNatGatewayDelete: (req) => {
          onNatGatewayDelete(req);
          return {} as never;
        },
      },
      routerEntries,
      session: { role },
    },
  );

describe('NatGatewaysListPage', () => {
  it('lists NAT gateways with their virtual network and external IP address', async () => {
    renderPage();

    const row = (await screen.findByText('nat-egress')).closest('tr') as HTMLElement;
    expect(screen.getAllByRole('columnheader').map((header) => header.textContent)).toEqual([
      'Name',
      'Status',
      'Virtual network',
      'External IP',
      'Project',
      '',
    ]);
    expect(within(row).getByRole('link', { name: 'vn-prod' })).toHaveAttribute(
      'href',
      '/networking/virtual-networks/vn-1',
    );
    // A virtual network has one NAT gateway, so its NAT gateway tab is the gateway's page.
    expect(within(row).getByRole('link', { name: 'nat-egress' })).toHaveAttribute(
      'href',
      '/networking/virtual-networks/vn-1?tab=nat-gateway',
    );
    // An external IP has no page, so its address is plain text.
    expect(await within(row).findByText('203.0.113.10')).toBeInTheDocument();
    expect(within(row).queryByRole('link', { name: '203.0.113.10' })).not.toBeInTheDocument();
    expect(within(row).getByText('Ready')).toBeInTheDocument();
  });

  it('disables Delete on the default NAT gateway and deletes another one', async () => {
    const onNatGatewayDelete = vi.fn();
    const { user } = renderPage('tenant-user', onNatGatewayDelete);

    const defaultRow = (await screen.findByText('default')).closest('tr') as HTMLElement;
    expect(within(defaultRow).getByText('Default')).toBeInTheDocument();
    await user.click(within(defaultRow).getByRole('button', { name: /Actions for/ }));
    const deleteDefault = screen.getByRole('menuitem', { name: 'Delete' });
    expect(deleteDefault).toHaveAttribute('aria-disabled', 'true');
    await user.hover(deleteDefault);
    expect(
      await screen.findByText('Created as a tenant default. It is deleted with the tenant.'),
    ).toBeInTheDocument();
    await user.keyboard('{Escape}');

    await user.click(screen.getByRole('button', { name: 'Actions for nat-egress' }));
    await user.click(screen.getByRole('menuitem', { name: 'Delete' }));
    const dialog = within(screen.getByRole('dialog'));
    expect(dialog.getByText('Delete nat-egress?')).toBeInTheDocument();
    await user.click(dialog.getByRole('button', { name: 'Delete' }));

    await waitFor(() =>
      expect(onNatGatewayDelete).toHaveBeenCalledWith(expect.objectContaining({ id: 'nat-1' })),
    );
  });

  it('creates a NAT gateway by picking an eligible virtual network', async () => {
    const { user } = renderPage();

    await screen.findByText('nat-egress');
    await user.click(screen.getByRole('button', { name: 'Create NAT gateway' }));
    const dialog = within(await screen.findByRole('dialog'));
    await user.click(dialog.getByLabelText(/^Virtual network/));

    expect(screen.getByRole('option', { name: /vn-free/ })).toBeInTheDocument();
    // Already has a NAT gateway, not ready, or a tenant default.
    expect(screen.queryByRole('option', { name: /vn-prod/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('option', { name: /vn-pending/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('option', { name: /vn-default/ })).not.toBeInTheDocument();

    await user.click(screen.getByRole('option', { name: /vn-free/ }));
    await user.click(dialog.getByRole('button', { name: 'Continue' }));

    expect(await screen.findByRole('heading', { name: 'Attach wizard' })).toBeInTheDocument();
  });

  it('names the tenant in the network options for a provider admin', async () => {
    const { user } = renderPage('admin');

    await screen.findByText('nat-egress');
    await user.click(screen.getByRole('button', { name: 'Create NAT gateway' }));
    const dialog = within(await screen.findByRole('dialog'));
    await user.click(dialog.getByLabelText(/^Virtual network/));

    expect(
      await screen.findByRole('option', { name: 'vn-free (10.0.0.0/16, tenant-a)' }),
    ).toBeInTheDocument();
  });

  it('offers Create NAT gateway in the empty state', async () => {
    renderPage('tenant-user', vi.fn(), { fixtureNatGateways: [] });

    expect(await screen.findByRole('heading', { name: 'No NAT gateways' })).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Create NAT gateway' })).toHaveLength(2);
  });

  it('shows one Clear all filters when the filters match nothing', async () => {
    const { user } = renderPage('tenant-user', vi.fn(), {
      routerEntries: ['/networking/nat-gateways?search=nomatch'],
    });

    expect(await screen.findByRole('heading', { name: 'No results found' })).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Clear all filters' })).toHaveLength(1);
    await user.click(screen.getByRole('button', { name: 'Clear all filters' }));
    expect(await screen.findByText('nat-egress')).toBeInTheDocument();
  });

  it('shows the Tenant column for a provider admin', async () => {
    renderPage('admin');

    expect(await screen.findByRole('columnheader', { name: 'Tenant' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Filter by tenant' })).toBeInTheDocument();
  });

  it('disables Delete while a cluster on its virtual network reaches the endpoints through it', async () => {
    const subnets = [
      { id: 'subnet-1', metadata: { name: 's1' }, spec: { virtualNetwork: { id: 'vn-1' } } },
    ] as unknown as Subnet[];
    const clusters = [
      {
        id: 'c-1',
        metadata: { name: 'c1' },
        spec: { networkAttachment: { subnet: { id: 'subnet-1' } } },
      },
    ] as unknown as Cluster[];
    const { user } = renderWithProviders(<NatGatewaysListPage />, {
      apiFixtures: { virtualNetworks, natGateways, externalIps, subnets, clusters },
      routerEntries: ['/networking/nat-gateways'],
      session: { role: 'tenant-user' },
    });

    await user.click(await screen.findByRole('button', { name: 'Actions for nat-egress' }));
    const deleteItem = screen.getByRole('menuitem', { name: 'Delete' });
    await waitFor(() => expect(deleteItem).toHaveAttribute('aria-disabled', 'true'));
    await user.hover(deleteItem);
    expect(
      await screen.findByText(
        'Cluster c1 reaches its management endpoints through this gateway. Delete it first.',
      ),
    ).toBeInTheDocument();
  });
});
