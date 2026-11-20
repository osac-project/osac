import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';

import type { SecurityGroup, VirtualNetwork } from '@osac/types';
import { Protocol, SecurityGroupState, VirtualNetworkState } from '@osac/types';
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

const renderPage = (
  securityGroups: SecurityGroup[] = mockSecurityGroups,
  virtualNetworks: VirtualNetwork[] = mockVirtualNetworks,
  routerEntries?: string[],
) =>
  renderWithProviders(<SecurityGroupsListPage />, {
    apiFixtures: { securityGroups, virtualNetworks },
    routerEntries,
  });

describe('SecurityGroupsListPage', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('renders page title and create button', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('Security groups')).toBeInTheDocument();
    });
    expect(screen.getByRole('button', { name: /Create security group/i })).toBeInTheDocument();
  });

  it('renders table with Name, VN, Inbound/Outbound Rules, Status columns', async () => {
    localStorage.setItem(getViewTypePrefKey('security-groups'), 'list');
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('Name')).toBeInTheDocument();
    });
    expect(screen.getByText('Virtual Network')).toBeInTheDocument();
    expect(screen.getByText('Inbound Rules')).toBeInTheDocument();
    expect(screen.getByText('Outbound Rules')).toBeInTheDocument();
    expect(screen.getByText('Created')).toBeInTheDocument();
    expect(screen.getByText('Status')).toBeInTheDocument();
  });

  it('displays security groups with rule counts', async () => {
    localStorage.setItem(getViewTypePrefKey('security-groups'), 'list');
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'sg-web' })).toBeInTheDocument();
      expect(screen.getAllByRole('link', { name: 'vn-prod' })).toHaveLength(2);
    });
    expect(screen.getByRole('link', { name: 'sg-db' })).toBeInTheDocument();

    const vnLinks = screen.getAllByRole('link', { name: 'vn-prod' });
    expect(vnLinks).toHaveLength(2);
    vnLinks.forEach((link) => {
      expect(link).toHaveAttribute('href', '/networking/virtual-networks/vn-1');
    });

    const rows = screen.getAllByRole('row');
    expect(rows).toHaveLength(3);
    expect(screen.getAllByRole('button', { name: /^Actions for / })).toHaveLength(2);
  });

  it('shows empty state when no security groups exist', async () => {
    renderPage([], mockVirtualNetworks);

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: 'No security groups found', level: 2 }),
      ).toBeInTheDocument();
    });
    expect(screen.getByText('No security groups are available yet.')).toBeInTheDocument();
  });

  it('filters security groups by search term', async () => {
    localStorage.setItem(getViewTypePrefKey('security-groups'), 'list');
    renderPage(mockSecurityGroups, mockVirtualNetworks, ['/networking/security-groups?search=web']);

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'sg-web' })).toBeInTheDocument();
    });
    expect(screen.queryByRole('link', { name: 'sg-db' })).not.toBeInTheDocument();
  });

  it('shows a filter-specific empty state when a search filter has no results', async () => {
    renderPage(mockSecurityGroups, mockVirtualNetworks, [
      '/networking/security-groups?search=missing',
    ]);

    await waitFor(() => {
      expect(
        screen.getByRole('heading', {
          name: 'No security groups match your filters',
          level: 2,
        }),
      ).toBeInTheDocument();
    });
    expect(screen.getByRole('button', { name: 'Clear all filters' })).toBeInTheDocument();
  });

  it('renders the list and card view switcher', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Card view' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'List view' })).toBeInTheDocument();
    });
  });

  it('opens create modal when Create button is clicked', async () => {
    const { user } = renderPage();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Create security group/i })).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: /Create security group/i }));
    expect(screen.getByRole('heading', { name: 'Create security group' })).toBeInTheDocument();
  });
});
