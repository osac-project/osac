import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { ExternalIP, NATGateway, Subnet, VirtualNetwork } from '@osac/types';
import { ExternalIPState, SubnetState, VirtualNetworkState } from '@osac/types';

import { VirtualNetworksListPage } from './VirtualNetworksListPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

const virtualNetworks = [
  {
    id: 'vn-1',
    metadata: { name: 'vn-prod' },
    spec: { ipv4Cidr: '10.0.0.0/16' },
    status: { state: VirtualNetworkState.READY },
  },
  {
    id: 'vn-2',
    metadata: { name: 'vn-dev' },
    spec: { ipv4Cidr: '10.1.0.0/16' },
    status: { state: VirtualNetworkState.PENDING },
  },
] as VirtualNetwork[];

const subnets = [
  {
    id: 'subnet-1',
    metadata: { name: 'subnet-a' },
    spec: { virtualNetwork: { id: 'vn-1' }, ipv4Cidr: '10.0.1.0/24' },
    status: { state: SubnetState.READY },
  },
] as Subnet[];

const natGateways = [
  {
    id: 'nat-1',
    metadata: { name: 'nat-egress' },
    spec: { virtualNetwork: { id: 'vn-1' }, externalIp: { id: 'eip-1' } },
  },
] as NATGateway[];

const externalIps = [
  {
    id: 'eip-1',
    metadata: { name: 'eip-1' },
    status: {
      state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED,
      attached: false,
      address: '203.0.113.10',
    },
  },
] as ExternalIP[];

const renderPage = (overrides?: {
  virtualNetworks?: VirtualNetwork[];
  natGateways?: NATGateway[];
  onNatGatewayList?: (req: unknown) => void;
}) =>
  renderWithProviders(<VirtualNetworksListPage />, {
    apiFixtures: {
      virtualNetworks: overrides?.virtualNetworks ?? virtualNetworks,
      subnets,
      natGateways: overrides?.natGateways ?? natGateways,
      externalIps,
    },
    transportOverrides: {
      onNatGatewayList: overrides?.onNatGatewayList,
    },
  });

describe('VirtualNetworksListPage', () => {
  it('renders the section label, title, and create button', async () => {
    renderPage();

    expect(screen.getByRole('heading', { name: 'Virtual networks' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Create virtual network' })).toBeInTheDocument();
    await screen.findByRole('link', { name: 'vn-prod' });
  });

  it('renders virtual network names as details links', async () => {
    renderPage();

    expect(await screen.findByRole('link', { name: 'vn-prod' })).toHaveAttribute(
      'href',
      '/networking/virtual-networks/vn-1',
    );
  });

  it('shows empty state when no virtual networks exist', async () => {
    renderPage({ virtualNetworks: [] });

    expect(await screen.findByText(/No virtual networks yet/i)).toBeInTheDocument();
  });

  it('shows the NAT gateway name and address, or a dash when none is attached', async () => {
    renderPage();

    expect(await screen.findByText('NAT gateway')).toBeInTheDocument();
    const table = screen.getByRole('grid');
    const rows = within(table).getAllByRole('row');
    const prodRow = rows.find((row) => within(row).queryByRole('link', { name: 'vn-prod' }));
    const devRow = rows.find((row) => within(row).queryByRole('link', { name: 'vn-dev' }));
    expect(prodRow).toBeDefined();
    expect(devRow).toBeDefined();

    const prodNatCell = within(prodRow as HTMLElement).getAllByRole('cell')[4];
    expect(within(prodNatCell).getByText('nat-egress')).toBeInTheDocument();
    expect(within(prodNatCell).getByText('203.0.113.10')).toBeInTheDocument();
    expect(within(prodNatCell).queryByText('Ready')).not.toBeInTheDocument();
    expect(within(devRow as HTMLElement).getByText('—')).toBeInTheDocument();
  });

  it('fetches NAT gateways once for the table', async () => {
    let listCalls = 0;
    renderPage({
      onNatGatewayList: () => {
        listCalls += 1;
      },
    });

    await screen.findByRole('link', { name: 'vn-prod' });
    await waitFor(() => {
      expect(listCalls).toBe(1);
    });
  });
});
