import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';

import type { ExternalIP, NATGateway, SecurityGroup, Subnet, VirtualNetwork } from '@osac/types';
import { ExternalIPState, SecurityGroupState, SubnetState, VirtualNetworkState } from '@osac/types';
import { type NetworkClass, NetworkClassState, type Tenant } from '@osac/types/private';
import { getViewTypePrefKey } from '@osac/ui-components/hooks/use-view-type';

import { VirtualNetworksListPage } from './VirtualNetworksListPage';
import type { UserRole } from '../../shellTypes';
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
    onSubnetList?: (req: unknown) => void;
    onSecurityGroupList?: (req: unknown) => void;
  },
  routerEntries?: string[],
  role: UserRole = 'tenant-user',
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
      onSubnetList: overrides?.onSubnetList,
      onSecurityGroupList: overrides?.onSecurityGroupList,
    },
    routerEntries,
    session: { role },
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
    expect(await screen.findByText('2 virtual networks')).toBeInTheDocument();
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

    expect(
      await screen.findByRole('heading', { name: 'No virtual networks', level: 2 }),
    ).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Create virtual network' })).toHaveLength(2);
  });

  it('shows a filter-specific empty state when the search matches nothing', async () => {
    renderPage({ virtualNetworks: [] }, ['/?search=no-such-vn']);

    expect(
      await screen.findByRole('heading', { name: 'No results found', level: 2 }),
    ).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Clear all filters' })).toHaveLength(1);
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

    expect(await screen.findByText('nat-egress')).toBeInTheDocument();
    expect(screen.getByText('203.0.113.10')).toBeInTheDocument();
    const table = screen.getByRole('grid');
    const rows = within(table).getAllByRole('row');
    const prodRow = rows.find((row) => within(row).queryByRole('link', { name: 'vn-prod' }));
    const devRow = rows.find((row) => within(row).queryByRole('link', { name: 'vn-dev' }));
    expect(prodRow).toBeDefined();
    expect(devRow).toBeDefined();

    const prodNatCell = getTableCellByLabel(prodRow as HTMLElement, 'NAT gateway');
    // One line: the gateway opens the network's NAT gateway tab; an external IP has no page.
    expect(prodNatCell).toHaveTextContent('nat-egress (203.0.113.10)');
    expect(within(prodNatCell).getByRole('link', { name: 'nat-egress' })).toHaveAttribute(
      'href',
      '/networking/virtual-networks/vn-1?tab=nat-gateway',
    );
    expect(
      within(prodNatCell).queryByRole('link', { name: '203.0.113.10' }),
    ).not.toBeInTheDocument();
    expect(within(prodNatCell).queryByText('Ready')).not.toBeInTheDocument();
    const devNatCell = getTableCellByLabel(devRow as HTMLElement, 'NAT gateway');
    expect(devNatCell).toHaveTextContent('—');
  });

  it('shows the project, the IPv4 CIDR and the NAT gateway on one line on a card', async () => {
    renderPage();

    const card = (await screen.findByRole('link', { name: 'vn-prod' })).closest(
      '.pf-v6-c-card',
    ) as HTMLElement;
    expect(within(card).getByText('IPv4 CIDR')).toBeInTheDocument();
    expect(within(card).queryByText('IPv4')).not.toBeInTheDocument();
    expect(within(card).getByText('Project')).toBeInTheDocument();
    // Without a project record to link to, the root project is still named Root.
    expect(within(card).getByText('Root')).toBeInTheDocument();
    const natLink = await within(card).findByRole('link', { name: 'nat-egress' });
    expect(natLink.parentElement).toHaveTextContent('nat-egress (203.0.113.10)');
  });

  it('shows subnet and security group counts in the table', async () => {
    localStorage.setItem(getViewTypePrefKey('virtual-networks'), 'list');
    renderPage();

    await screen.findByRole('link', { name: 'vn-prod' });
    const table = screen.getByRole('grid');
    const prodRow = within(table)
      .getAllByRole('row')
      .find((row) => within(row).queryByRole('link', { name: 'vn-prod' }));
    expect(prodRow).toBeDefined();

    await waitFor(() => {
      expect(getTableCellByLabel(prodRow as HTMLElement, 'Subnets')).toHaveTextContent('1');
    });
    expect(getTableCellByLabel(prodRow as HTMLElement, 'Security groups')).toHaveTextContent('1');
  });

  it('shows unavailable subnet and security group counts when list requests fail', async () => {
    localStorage.setItem(getViewTypePrefKey('virtual-networks'), 'list');
    renderPage({
      onSubnetList: () => {
        throw new ConnectError('subnets unavailable', Code.Unavailable);
      },
      onSecurityGroupList: () => {
        throw new ConnectError('security groups unavailable', Code.Unavailable);
      },
    });

    await screen.findByRole('link', { name: 'vn-prod' });
    const table = screen.getByRole('grid');
    const prodRow = within(table)
      .getAllByRole('row')
      .find((row) => within(row).queryByRole('link', { name: 'vn-prod' }));
    expect(prodRow).toBeDefined();

    const prodRowElement = prodRow as HTMLElement;

    await waitFor(() => {
      expect(
        within(prodRowElement).getByRole('button', { name: 'Failed to load subnets' }),
      ).toBeInTheDocument();
      expect(
        within(prodRowElement).getByRole('button', { name: 'Failed to load security groups' }),
      ).toBeInTheDocument();
    });

    const subnetsCell = getTableCellByLabel(prodRowElement, 'Subnets');
    const securityGroupsCell = getTableCellByLabel(prodRowElement, 'Security groups');
    expect(within(subnetsCell).queryByText('0')).not.toBeInTheDocument();
    expect(within(securityGroupsCell).queryByText('0')).not.toBeInTheDocument();
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

  it('shows the tenant and the Default badge to a cloud provider admin in the table view', async () => {
    localStorage.setItem(getViewTypePrefKey('virtual-networks'), 'list');
    renderWithProviders(<VirtualNetworksListPage />, {
      apiFixtures: {
        virtualNetworks: [
          {
            id: 'vn-default',
            metadata: {
              name: 'default',
              tenant: 'tenant-a',
              labels: { 'osac.openshift.io/default': 'true' },
            },
            spec: { ipv4Cidr: '10.200.0.0/16' },
            status: { state: VirtualNetworkState.READY },
          },
        ] as unknown as VirtualNetwork[],
        subnets: [],
        securityGroups: [],
        natGateways: [],
        externalIps: [],
      },
      session: { role: 'admin', tenantId: '' },
    });

    expect(await screen.findByRole('columnheader', { name: 'Tenant' })).toBeInTheDocument();
    const row = (await screen.findByRole('link', { name: 'default' })).closest('tr') as HTMLElement;
    expect(getTableCellByLabel(row, 'Tenant')).toHaveTextContent('tenant-a');
    expect(within(row).getByText('Default')).toBeInTheDocument();
  });

  it('filters by tenant and shows the tenant on cards for a provider admin', async () => {
    const tenants = [
      { id: 'tenant-a', metadata: { name: 'Acme' } },
      { id: 'tenant-b', metadata: { name: 'Globex' } },
    ] as Tenant[];
    const { user } = renderWithProviders(<VirtualNetworksListPage />, {
      apiFixtures: {
        virtualNetworks: [
          { ...virtualNetworks[0], metadata: { name: 'default', tenant: 'tenant-a' } },
          { ...virtualNetworks[1], metadata: { name: 'default', tenant: 'tenant-b' } },
        ] as VirtualNetwork[],
        subnets: [],
        securityGroups: [],
        natGateways: [],
        externalIps: [],
        tenants,
      },
      session: { role: 'admin', tenantId: '' },
    });

    expect(
      await screen.findByText('Networks for workloads and catalog networking.'),
    ).toBeInTheDocument();
    expect(await screen.findAllByRole('link', { name: 'default' })).toHaveLength(2);
    expect(await screen.findByText('Acme')).toBeInTheDocument();
    expect(screen.getByText('Globex')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Filter by tenant' }));
    await user.click(screen.getByRole('option', { name: 'Globex' }));

    await waitFor(() => expect(screen.getAllByRole('link', { name: 'default' })).toHaveLength(1));
    const card = screen.getByRole('link', { name: 'default' }).closest('.pf-v6-c-card');
    expect(within(card as HTMLElement).getByText('Globex')).toBeInTheDocument();
    expect(screen.queryByText('Acme')).not.toBeInTheDocument();
  });

  it('puts the Tenant column last for a provider admin', async () => {
    localStorage.setItem(getViewTypePrefKey('virtual-networks'), 'list');
    renderPage(undefined, undefined, 'admin');

    await screen.findByRole('link', { name: 'vn-prod' });
    const headers = screen.getAllByRole('columnheader').map((header) => header.textContent);
    expect(headers.slice(-2)).toEqual(['Tenant', '']);
  });

  it('shows no Tenant filter to a tenant user', async () => {
    renderPage();

    await screen.findByRole('link', { name: 'vn-prod' });
    expect(screen.queryByRole('button', { name: 'Filter by tenant' })).not.toBeInTheDocument();
    expect(screen.queryByText('Tenant')).not.toBeInTheDocument();
  });

  it('shows no Tenant column to a tenant user', async () => {
    localStorage.setItem(getViewTypePrefKey('virtual-networks'), 'list');
    renderWithProviders(<VirtualNetworksListPage />, {
      apiFixtures: { virtualNetworks, subnets, securityGroups, natGateways, externalIps },
      session: { role: 'tenant-user', tenantId: 'tenant-a' },
    });

    await screen.findByRole('link', { name: 'vn-prod' });
    expect(screen.queryByRole('columnheader', { name: 'Tenant' })).not.toBeInTheDocument();
  });

  describe('network class gate', () => {
    const renderWithClass = (role: UserRole, state: NetworkClassState) =>
      renderWithProviders(<VirtualNetworksListPage />, {
        apiFixtures: {
          virtualNetworks,
          subnets,
          securityGroups,
          natGateways,
          externalIps,
          networkClasses: [{ id: 'nc-1', status: { state } } as unknown as NetworkClass],
        },
        session: { role, tenantId: '' },
      });

    it('disables Create virtual network for the provider admin until the network class is Ready', async () => {
      const { user } = renderWithClass('admin', NetworkClassState.PENDING);
      expect(await screen.findAllByText('vn-prod')).not.toHaveLength(0);

      await waitFor(() =>
        expect(screen.getByRole('button', { name: 'Create virtual network' })).toHaveAttribute(
          'aria-disabled',
          'true',
        ),
      );
      const create = screen.getByRole('button', { name: 'Create virtual network' });
      await user.hover(create);
      expect(
        await screen.findByText('Available when the network class is Ready.'),
      ).toBeInTheDocument();
    });

    it('enables Create virtual network for the provider admin when the network class is Ready', async () => {
      renderWithClass('admin', NetworkClassState.READY);

      const create = await screen.findByRole('button', { name: 'Create virtual network' });
      await waitFor(() => expect(create).not.toHaveAttribute('aria-disabled', 'true'));
    });

    it('does not gate a tenant, who cannot read the network class', async () => {
      renderWithClass('tenant-admin', NetworkClassState.PENDING);

      const create = await screen.findByRole('button', { name: 'Create virtual network' });
      await waitFor(() => expect(create).not.toHaveAttribute('aria-disabled', 'true'));
    });
  });
});
