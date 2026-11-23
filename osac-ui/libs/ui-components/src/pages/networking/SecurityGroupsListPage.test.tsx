import { screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';

import type { SecurityGroup, VirtualNetwork } from '@osac/types';
import { Protocol, SecurityGroupState, VirtualNetworkState } from '@osac/types';
import { buildSecurityGroupListFilter } from '@osac/ui-components/components/networking/security-group-list-utils';
import { getViewTypePrefKey } from '@osac/ui-components/hooks/use-view-type';

import { SecurityGroupsListPage } from './SecurityGroupsListPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

const mockVirtualNetworks = [
  {
    id: 'vn-1',
    metadata: { name: 'vn-prod' },
    spec: { ipv4Cidr: '10.0.0.0/16' },
    status: { state: VirtualNetworkState.READY },
  },
] as VirtualNetwork[];

const mockSecurityGroups = [
  {
    id: 'sg-1',
    metadata: { name: 'sg-web' },
    spec: {
      virtualNetwork: { id: 'vn-1' },
      ingress: [{ protocol: Protocol.TCP, portFrom: 80, portTo: 80 }],
      egress: [],
    },
    status: { state: SecurityGroupState.READY },
  },
  {
    id: 'sg-2',
    metadata: { name: 'sg-db' },
    spec: {
      virtualNetwork: { id: 'vn-1' },
      ingress: [{ protocol: Protocol.TCP, portFrom: 3306, portTo: 3306 }],
      egress: [{ protocol: Protocol.ALL }],
    },
    status: { state: SecurityGroupState.PENDING },
  },
] as SecurityGroup[];

type RenderOptions = {
  securityGroups?: SecurityGroup[];
  virtualNetworks?: VirtualNetwork[];
  onSecurityGroupList?: (req: unknown) => void;
  routerEntries?: string[];
};

const renderPage = (options?: RenderOptions) =>
  renderWithProviders(<SecurityGroupsListPage />, {
    apiFixtures: {
      virtualNetworks: options?.virtualNetworks ?? mockVirtualNetworks,
      securityGroups: options?.securityGroups ?? mockSecurityGroups,
    },
    transportOverrides: {
      onSecurityGroupList: options?.onSecurityGroupList,
    },
    routerEntries: options?.routerEntries,
  });

const getTableCellByLabel = (row: HTMLElement, label: string) => {
  const cell = within(row)
    .getAllByRole('cell')
    .find((c) => c.getAttribute('data-label') === label);
  expect(cell).toBeDefined();
  return cell as HTMLElement;
};

describe('SecurityGroupsListPage', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('renders page title, description, and create button', async () => {
    renderPage();

    expect(screen.getByRole('heading', { name: 'Security groups' })).toBeInTheDocument();
    expect(
      screen.getByText('Manage firewall rules for your virtual networks.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Create security group' })).toBeInTheDocument();
    await screen.findByRole('link', { name: 'sg-web' });
  });

  it('renders the list and card view switcher', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Card view' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'List view' })).toBeInTheDocument();
    });
  });

  it('renders security groups as cards by default', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'sg-web' })).toBeInTheDocument();
      expect(screen.getByRole('link', { name: 'sg-db' })).toBeInTheDocument();
    });
    expect(screen.getAllByText('Project').length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText('Inbound Rules').length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByRole('grid', { name: 'Security groups' })).not.toBeInTheDocument();
  });

  it('renders the project filter', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Project:/i })).toBeInTheDocument();
    });
  });

  it('renders table with Name, Status, Project, Virtual Network, rule, and Created columns in list view', async () => {
    localStorage.setItem(getViewTypePrefKey('security-groups'), 'list');
    renderPage();

    const table = await screen.findByRole('grid', { name: 'Security groups' });
    expect(within(table).getByText('Name')).toBeInTheDocument();
    expect(within(table).getByText('Status')).toBeInTheDocument();
    expect(within(table).getByText('Project')).toBeInTheDocument();
    expect(within(table).getByText('Virtual Network')).toBeInTheDocument();
    expect(within(table).getByText('Inbound Rules')).toBeInTheDocument();
    expect(within(table).getByText('Outbound Rules')).toBeInTheDocument();
    expect(within(table).getByText('Created')).toBeInTheDocument();
  });

  it('displays security groups with virtual network links and rule counts in list view', async () => {
    localStorage.setItem(getViewTypePrefKey('security-groups'), 'list');
    renderPage();

    await screen.findByRole('link', { name: 'sg-web' });
    const vnLinks = await waitFor(() => {
      const links = screen.getAllByRole('link', { name: 'vn-prod' });
      expect(links).toHaveLength(2);
      return links;
    });
    vnLinks.forEach((link) => {
      expect(link).toHaveAttribute('href', '/networking/virtual-networks/vn-1');
    });

    const table = screen.getByRole('grid', { name: 'Security groups' });
    const webRow = within(table)
      .getAllByRole('row')
      .find((row) => within(row).queryByRole('link', { name: 'sg-web' }));
    const dbRow = within(table)
      .getAllByRole('row')
      .find((row) => within(row).queryByRole('link', { name: 'sg-db' }));
    expect(webRow).toBeDefined();
    expect(dbRow).toBeDefined();

    expect(getTableCellByLabel(webRow as HTMLElement, 'Project')).toHaveTextContent('Default');
    expect(getTableCellByLabel(dbRow as HTMLElement, 'Project')).toHaveTextContent('Default');
    expect(getTableCellByLabel(webRow as HTMLElement, 'Inbound Rules')).toHaveTextContent('1');
    expect(getTableCellByLabel(webRow as HTMLElement, 'Outbound Rules')).toHaveTextContent('0');
    expect(getTableCellByLabel(dbRow as HTMLElement, 'Inbound Rules')).toHaveTextContent('1');
    expect(getTableCellByLabel(dbRow as HTMLElement, 'Outbound Rules')).toHaveTextContent('1');

    expect(within(table).getAllByRole('row')).toHaveLength(3);
  });

  it('shows an empty state when no security groups exist', async () => {
    renderPage({ securityGroups: [] });

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: 'No security groups found', level: 2 }),
      ).toBeInTheDocument();
    });
    expect(screen.getByText('No security groups are available yet.')).toBeInTheDocument();
  });

  it('shows a filter-specific empty state when the search matches nothing', async () => {
    renderPage({ securityGroups: [], routerEntries: ['/?search=no-such-sg'] });

    await waitFor(() => {
      expect(
        screen.getByRole('heading', {
          name: 'No security groups match your filters',
          level: 2,
        }),
      ).toBeInTheDocument();
    });
    expect(screen.getByText('Try a different project or search term.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Clear all filters' })).toBeInTheDocument();
  });

  it('sends a CEL filter when filtering security groups by search', async () => {
    let lastFilter: string | undefined;
    const { user } = renderPage({
      onSecurityGroupList: (req) => {
        lastFilter = (req as { filter?: string }).filter;
      },
    });

    await screen.findByRole('link', { name: 'sg-web' });

    const searchInput = screen.getByRole('textbox', { name: 'Search security groups' });
    await user.type(searchInput, 'web');

    await waitFor(() => {
      expect(lastFilter).toBe(buildSecurityGroupListFilter(undefined, 'web'));
      expect(screen.queryByRole('link', { name: 'sg-db' })).not.toBeInTheDocument();
    });
    expect(screen.getByRole('link', { name: 'sg-web' })).toBeInTheDocument();
  });

  it('opens create modal when Create button is clicked', async () => {
    const { user } = renderPage();

    await screen.findByRole('link', { name: 'sg-web' });
    await user.click(screen.getByRole('button', { name: 'Create security group' }));
    expect(screen.getByRole('heading', { name: 'Create security group' })).toBeInTheDocument();
  });
});
