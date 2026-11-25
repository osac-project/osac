import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { Subnet, VirtualNetwork } from '@osac/types';
import { SubnetState, VirtualNetworkState } from '@osac/types';
import { buildSubnetListFilter } from '@osac/ui-components/components/networking/subnet-list-utils';

import { SubnetsListPage } from './SubnetsListPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

const mockVirtualNetworks = [
  {
    id: 'vn-1',
    metadata: { name: 'app-net', project: 'web-team' },
    spec: { ipv4Cidr: '10.20.0.0/16' },
    status: { state: VirtualNetworkState.READY },
  },
  {
    id: 'vn-2',
    metadata: { name: 'db-net', project: 'data-team' },
    spec: { ipv4Cidr: '10.30.0.0/16' },
    status: { state: VirtualNetworkState.READY },
  },
] as VirtualNetwork[];

const mockSubnets = [
  {
    id: 'subnet-1',
    metadata: { name: 'web-a', project: 'web-team' },
    spec: { ipv4Cidr: '10.20.1.0/24', virtualNetwork: { id: 'vn-1' } },
    status: { state: SubnetState.READY },
  },
  {
    id: 'subnet-2',
    metadata: { name: 'db-a', project: 'data-team' },
    spec: { ipv4Cidr: '10.30.1.0/24', virtualNetwork: { id: 'vn-2' } },
    status: { state: SubnetState.PENDING },
  },
] as Subnet[];

type RenderOptions = {
  subnets?: Subnet[];
  virtualNetworks?: VirtualNetwork[];
  onSubnetList?: (req: unknown) => void;
  routerEntries?: string[];
};

const renderPage = (options: RenderOptions = {}) =>
  renderWithProviders(<SubnetsListPage />, {
    apiFixtures: {
      virtualNetworks: options.virtualNetworks ?? mockVirtualNetworks,
      subnets: options.subnets ?? mockSubnets,
    },
    transportOverrides: { onSubnetList: options.onSubnetList },
    routerEntries: options.routerEntries,
  });

const getTableCellByLabel = (row: HTMLElement, label: string) => {
  const cell = within(row)
    .getAllByRole('cell')
    .find((candidate) => candidate.getAttribute('data-label') === label);
  expect(cell).toBeDefined();
  return cell as HTMLElement;
};

describe('SubnetsListPage', () => {
  it('renders the page title, description, create link and columns', async () => {
    renderPage();

    expect(screen.getByRole('heading', { name: 'Subnets' })).toBeInTheDocument();
    expect(screen.getByText('Address ranges inside virtual networks.')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Create subnet' })).toHaveAttribute(
      'href',
      '/networking/subnets/create',
    );

    const table = await screen.findByRole('grid', { name: 'Subnets' });
    for (const column of ['Name', 'Status', 'IPv4 CIDR', 'Virtual network', 'Project']) {
      expect(within(table).getByText(column)).toBeInTheDocument();
    }
    expect(within(table).getByText('web-a')).toBeInTheDocument();
    expect(within(table).getByText('db-a')).toBeInTheDocument();
  });

  it('links each row to its virtual network and shows the project', async () => {
    renderPage();

    await screen.findByRole('link', { name: 'app-net' });
    expect(screen.getByRole('link', { name: 'app-net' })).toHaveAttribute(
      'href',
      '/networking/virtual-networks/vn-1',
    );
    expect(screen.getByRole('link', { name: 'db-net' })).toHaveAttribute(
      'href',
      '/networking/virtual-networks/vn-2',
    );

    const table = screen.getByRole('grid', { name: 'Subnets' });
    const webRow = within(table)
      .getAllByRole('row')
      .find((row) => within(row).queryByText('web-a'));
    expect(getTableCellByLabel(webRow as HTMLElement, 'Project')).toHaveTextContent('web-team');
    expect(getTableCellByLabel(webRow as HTMLElement, 'IPv4 CIDR')).toHaveTextContent(
      '10.20.1.0/24',
    );
  });

  it('shows Root for a subnet in the tenant default project', async () => {
    renderPage({
      subnets: [
        { ...mockSubnets[0], metadata: { name: 'default', project: '' } } as unknown as Subnet,
      ],
    });

    await screen.findByText('default');
    expect(screen.getByText('Root')).toBeInTheDocument();
  });

  it('filters by the virtual network in the URL', async () => {
    renderPage({ routerEntries: ['/networking/subnets?virtualNetwork=vn-1'] });

    await screen.findByText('web-a');
    expect(screen.queryByText('db-a')).not.toBeInTheDocument();
    expect(screen.getByText('app-net (10.20.0.0/16)')).toBeInTheDocument();
  });

  it('sends a CEL filter when searching by name', async () => {
    let lastFilter: string | undefined;
    const { user } = renderPage({
      onSubnetList: (request) => {
        lastFilter = (request as { filter?: string }).filter;
      },
    });

    await screen.findByText('web-a');
    await user.type(screen.getByRole('textbox', { name: 'Search subnets by name' }), 'web');

    await waitFor(() => {
      expect(lastFilter).toBe(buildSubnetListFilter(undefined, 'web'));
      expect(screen.queryByText('db-a')).not.toBeInTheDocument();
    });
    expect(screen.getByText('web-a')).toBeInTheDocument();
  });

  it('shows the empty state when no subnet exists', async () => {
    renderPage({ subnets: [] });

    expect(
      await screen.findByRole('heading', { name: 'No subnets found', level: 2 }),
    ).toBeInTheDocument();
    expect(screen.getByText('No subnets are available yet.')).toBeInTheDocument();
  });

  it('shows a filter-specific empty state when the filters match nothing', async () => {
    renderPage({
      subnets: [],
      routerEntries: ['/networking/subnets?virtualNetwork=vn-1'],
    });

    expect(
      await screen.findByRole('heading', { name: 'No subnets match your filters', level: 2 }),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Clear all filters' })).toBeInTheDocument();
  });

  it('offers the row delete action', async () => {
    const { user } = renderPage();

    await screen.findByText('web-a');
    await user.click(screen.getByRole('button', { name: 'Actions for web-a' }));

    expect(screen.getByRole('menuitem', { name: 'Delete subnet' })).toBeInTheDocument();
  });
});
