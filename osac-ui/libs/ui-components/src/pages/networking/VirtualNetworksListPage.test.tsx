import { screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { ExternalIP, NATGateway, SecurityGroup, Subnet, VirtualNetwork } from '@osac/types';
import { ExternalIPState, SecurityGroupState, SubnetState, VirtualNetworkState } from '@osac/types';
import { getViewTypePrefKey } from '@osac/ui-components/hooks/use-view-type';

import { VirtualNetworksListPage } from './VirtualNetworksListPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) =>
  window.setTimeout(() => callback(0), 0),
);

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
  {
    id: 'subnet-2',
    metadata: { name: 'subnet-b' },
    spec: { virtualNetwork: { id: 'vn-1' }, ipv4Cidr: '10.0.2.0/24' },
    status: { state: SubnetState.READY },
  },
] as Subnet[];

const securityGroups = [
  {
    id: 'sg-1',
    metadata: { name: 'sg-web' },
    spec: { virtualNetwork: { id: 'vn-1' }, ingress: [], egress: [] },
    status: { state: SecurityGroupState.READY },
  },
] as unknown as SecurityGroup[];

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

const renderPage = (
  overrides?: {
    virtualNetworks?: VirtualNetwork[];
    natGateways?: NATGateway[];
    onNatGatewayList?: (req: unknown) => void;
  },
  routerEntries?: string[],
) =>
  renderWithProviders(<VirtualNetworksListPage />, {
    apiFixtures: {
      virtualNetworks: overrides?.virtualNetworks ?? virtualNetworks,
      subnets,
      securityGroups,
      natGateways: overrides?.natGateways ?? natGateways,
      externalIps,
    },
    transportOverrides: {
      onNatGatewayList: overrides?.onNatGatewayList,
    },
    routerEntries,
  });

const getTableCellByLabel = (row: HTMLElement, label: string) => {
  const cell = within(row)
    .getAllByRole('cell')
    .find((c) => c.getAttribute('data-label') === label);
  expect(cell).toBeDefined();
  return cell as HTMLElement;
};

describe('VirtualNetworksListPage', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('renders the section label, title, and create button', async () => {
    renderPage();

    expect(screen.getByRole('heading', { name: 'Virtual networks' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Create virtual network' })).toBeInTheDocument();
    await screen.findByRole('link', { name: 'vn-prod' });
    expect(screen.getByText('2 virtual networks')).toBeInTheDocument();
  });

  it('renders virtual network names as details links', async () => {
    renderPage();

    expect(await screen.findByRole('link', { name: 'vn-prod' })).toHaveAttribute(
      'href',
      '/networking/virtual-networks/vn-1',
    );
  });

  it('shows an empty state when no virtual networks exist', async () => {
    renderPage({ virtualNetworks: [] });

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: 'No virtual networks found', level: 2 }),
      ).toBeInTheDocument();
    });
    expect(screen.getByText('No virtual networks are available yet.')).toBeInTheDocument();
  });

  it('shows a filter-specific empty state when the search matches nothing', async () => {
    renderPage({ virtualNetworks: [] }, ['/?search=no-such-vn']);

    await waitFor(() => {
      expect(
        screen.getByRole('heading', {
          name: 'No virtual networks match your filters',
          level: 2,
        }),
      ).toBeInTheDocument();
    });
    expect(screen.getByText('Try a different state or search term.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Clear all filters' })).toBeInTheDocument();
  });

  it('renders the state filter toggle', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'All states' })).toBeInTheDocument();
    });
  });

  it('renders the list and card view switcher', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Card view' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'List view' })).toBeInTheDocument();
    });
  });

  it('shows the NAT gateway name and address, or a dash when none is attached', async () => {
    localStorage.setItem(getViewTypePrefKey('virtual-networks'), 'list');
    renderPage();

    expect(await screen.findByText('NAT gateway')).toBeInTheDocument();
    const table = screen.getByRole('grid');
    const rows = within(table).getAllByRole('row');
    const prodRow = rows.find((row) => within(row).queryByRole('link', { name: 'vn-prod' }));
    const devRow = rows.find((row) => within(row).queryByRole('link', { name: 'vn-dev' }));
    expect(prodRow).toBeDefined();
    expect(devRow).toBeDefined();

    const prodNatCell = getTableCellByLabel(prodRow as HTMLElement, 'NAT gateway');
    expect(within(prodNatCell).getByText('nat-egress')).toBeInTheDocument();
    expect(within(prodNatCell).getByText('203.0.113.10')).toBeInTheDocument();
    expect(within(prodNatCell).queryByText('Ready')).not.toBeInTheDocument();
    const devNatCell = getTableCellByLabel(devRow as HTMLElement, 'NAT gateway');
    expect(devNatCell).toHaveTextContent('—');
  });

  it('shows subnet and security group counts in the table', async () => {
    localStorage.setItem(getViewTypePrefKey('virtual-networks'), 'list');
    renderPage();

    expect(await screen.findByText('Security groups')).toBeInTheDocument();
    const table = screen.getByRole('grid');
    await waitFor(() => {
      const rows = within(table).getAllByRole('row');
      const prodRow = rows.find((row) => within(row).queryByRole('link', { name: 'vn-prod' }));
      expect(prodRow).toBeDefined();
      expect(getTableCellByLabel(prodRow as HTMLElement, 'Subnets')).toHaveTextContent('2');
      expect(getTableCellByLabel(prodRow as HTMLElement, 'Security groups')).toHaveTextContent('1');
    });
  });

  it('shows subnet and security group counts on cards', async () => {
    localStorage.setItem(getViewTypePrefKey('virtual-networks'), 'cards');
    renderPage();

    const prodCard = (await screen.findByRole('link', { name: 'vn-prod' })).closest(
      '.pf-v6-c-card',
    );
    expect(prodCard).toBeDefined();
    await waitFor(() => {
      expect(within(prodCard as HTMLElement).getByText('Subnets')).toBeInTheDocument();
      const terms = within(prodCard as HTMLElement).getAllByText('Subnets');
      const subnetGroup = terms[0].closest('.pf-v6-c-description-list__group');
      expect(subnetGroup).toHaveTextContent('2');
      const securityGroupTerm = within(prodCard as HTMLElement).getByText('Security groups');
      expect(securityGroupTerm.closest('.pf-v6-c-description-list__group')).toHaveTextContent('1');
    });
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
