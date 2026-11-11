import { screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { SecurityGroup, VirtualNetwork } from '@osac/types';
import { Protocol, SecurityGroupState, VirtualNetworkState } from '@osac/types';
import type { Tenant } from '@osac/types/private';

import { SecurityGroupsListPage } from './SecurityGroupsListPage';
import * as networkingApi from '../../api/v1/networking';
import type { UserRole } from '../../shellTypes';
import { mockMutationResult, mockQueryResult } from '../../test-utils/query';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.mock('../../api/v1/networking', async (importOriginal) => {
  const actual = await importOriginal<typeof networkingApi>();
  return {
    ...actual,
    useSecurityGroups: vi.fn(),
    useVirtualNetworks: vi.fn(),
    useCreateSecurityGroup: vi.fn(),
  };
});

const renderPage = (role: UserRole = 'tenant-user', routerEntries?: string[]) =>
  renderWithProviders(<SecurityGroupsListPage />, {
    session: { role },
    routerEntries,
    apiFixtures: {
      tenants: [
        { id: 'tenant-a', metadata: { name: 'Acme' } },
        { id: 'tenant-b', metadata: { name: 'Globex' } },
      ] as Tenant[],
    },
  });

describe('SecurityGroupsListPage', () => {
  const mockVirtualNetworks = [
    {
      id: 'vn-2',
      metadata: { name: 'vn-dev' },
      spec: { ipv4Cidr: '10.1.0.0/16' },
      status: { state: VirtualNetworkState.READY },
    },
    {
      id: 'vn-1',
      metadata: { name: 'vn-prod' },
      spec: { ipv4Cidr: '10.0.0.0/16' },
      status: { state: VirtualNetworkState.READY },
    },
  ];

  const mockSecurityGroups = [
    {
      id: 'sg-1',
      metadata: { name: 'sg-web', tenant: 'tenant-a' },
      spec: {
        virtualNetwork: { id: 'vn-1' },
        ingress: [{ protocol: Protocol.TCP, portFrom: 80, portTo: 80 }],
        egress: [],
      },
      status: { state: SecurityGroupState.READY },
    },
    {
      id: 'sg-3',
      metadata: { name: 'sg-dev', tenant: 'tenant-b' },
      spec: { virtualNetwork: { id: 'vn-2' }, ingress: [], egress: [] },
      status: { state: SecurityGroupState.READY },
    },
    {
      id: 'sg-2',
      metadata: { name: 'sg-db', tenant: 'tenant-a' },
      spec: {
        virtualNetwork: { id: 'vn-1' },
        ingress: [{ protocol: Protocol.TCP, portFrom: 3306, portTo: 3306 }],
        egress: [{ protocol: Protocol.ALL }],
      },
      status: { state: SecurityGroupState.PENDING },
    },
  ];

  beforeEach(() => {
    vi.mocked(networkingApi.useVirtualNetworks).mockReturnValue(
      mockQueryResult<VirtualNetwork[]>({ data: mockVirtualNetworks as VirtualNetwork[] }),
    );

    vi.mocked(networkingApi.useSecurityGroups).mockReturnValue(
      mockQueryResult<SecurityGroup[]>({ data: mockSecurityGroups as SecurityGroup[] }),
    );

    vi.mocked(networkingApi.useCreateSecurityGroup).mockReturnValue(
      mockMutationResult({ mutateAsync: vi.fn() }),
    );
  });

  it('shows the Auto badge with its owner tooltip and the Default badge', async () => {
    vi.mocked(networkingApi.useSecurityGroups).mockReturnValue(
      mockQueryResult<SecurityGroup[]>({
        data: [
          {
            ...mockSecurityGroups[0],
            metadata: {
              name: 'auto-sg-c1',
              labels: {
                'osac.openshift.io/auto-created': 'true',
                'osac.openshift.io/auto-created-for': 'c1',
                'osac.openshift.io/auto-created-kind': 'cluster',
              },
            },
          },
          {
            ...mockSecurityGroups[1],
            metadata: { name: 'default', labels: { 'osac.openshift.io/default': 'true' } },
          },
        ] as unknown as SecurityGroup[],
      }),
    );
    const { user } = renderPage();

    expect(screen.getByText('Default')).toBeInTheDocument();
    await user.hover(screen.getByText('Auto'));
    expect(
      await screen.findByText('Created automatically for cluster c1. It is deleted with it.'),
    ).toBeInTheDocument();
  });

  it('renders page title and create button', () => {
    renderPage();

    expect(screen.getByText('Security groups')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Create security group/i })).toBeInTheDocument();
  });

  it('renders table with Name, VN, Inbound/Outbound Rules, Status columns', () => {
    renderPage();

    expect(screen.getByText('Name')).toBeInTheDocument();
    expect(screen.getByText('Virtual network')).toBeInTheDocument();
    expect(screen.getByText('Inbound rules')).toBeInTheDocument();
    expect(screen.getByText('Outbound rules')).toBeInTheDocument();
    expect(screen.getByText('Status')).toBeInTheDocument();
    expect(screen.queryByRole('columnheader', { name: 'Tenant' })).not.toBeInTheDocument();
  });

  it('wraps the rule count headers instead of cutting them off', () => {
    renderPage();

    expect(screen.getByRole('columnheader', { name: 'Inbound rules' })).toHaveClass('pf-m-wrap');
    expect(screen.getByRole('columnheader', { name: 'Outbound rules' })).toHaveClass('pf-m-wrap');
    expect(screen.getByRole('columnheader', { name: 'Project' })).toBeInTheDocument();
  });

  it('links the group name in the plain link style of every networking link', async () => {
    renderPage();

    const link = await screen.findByRole('link', { name: 'sg-web' });
    expect(link).not.toHaveClass('pf-v6-u-font-weight-bold');
  });

  it('filters by the virtual network in the URL and clears the filter', async () => {
    const { user } = renderPage('tenant-user', ['/networking/security-groups?virtualNetwork=vn-2']);

    expect(screen.getByRole('link', { name: 'sg-dev' })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'sg-web' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Filter by virtual network' })).toHaveTextContent(
      'vn-dev',
    );

    await user.click(screen.getByRole('button', { name: 'Clear all filters' }));
    expect(await screen.findByRole('link', { name: 'sg-web' })).toBeInTheDocument();
  });

  it('shows the no-results state with Clear all filters', async () => {
    const { user } = renderPage('tenant-user', ['/networking/security-groups?search=nothing']);

    expect(screen.getByRole('heading', { name: 'No results found' })).toBeInTheDocument();
    // One Clear all filters: the toolbar link is hidden while the empty state offers it.
    expect(screen.getAllByRole('button', { name: 'Clear all filters' })).toHaveLength(1);
    await user.click(screen.getByRole('button', { name: 'Clear all filters' }));
    expect(await screen.findByRole('link', { name: 'sg-web' })).toBeInTheDocument();
  });

  it('shows the Tenant column and filters by tenant for a provider admin', async () => {
    const { user } = renderPage('admin');

    expect(screen.getByRole('columnheader', { name: 'Tenant' })).toBeInTheDocument();
    expect(await screen.findAllByText('Acme')).toHaveLength(2);

    await user.click(screen.getByRole('button', { name: 'Filter by tenant' }));
    await user.click(screen.getByRole('option', { name: 'Globex' }));

    expect(screen.getByRole('link', { name: 'sg-dev' })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'sg-web' })).not.toBeInTheDocument();
  });

  it('displays security groups with rule counts', () => {
    renderPage();

    expect(screen.getByRole('link', { name: 'sg-web' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'sg-db' })).toBeInTheDocument();

    const vnLinks = screen.getAllByRole('link', { name: 'vn-prod' });
    expect(vnLinks).toHaveLength(2);
    vnLinks.forEach((link) => {
      expect(link).toHaveAttribute('href', '/networking/virtual-networks/vn-1');
    });

    const rows = screen.getAllByRole('row');
    expect(rows).toHaveLength(4); // header + 3 data rows
  });

  it('shows empty state when no security groups exist', () => {
    vi.mocked(networkingApi.useSecurityGroups).mockReturnValue(mockQueryResult<SecurityGroup[]>());

    renderPage();

    expect(screen.getByRole('heading', { name: 'No security groups' })).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Create security group' })).toHaveLength(2);
  });

  it('filters security groups by search term', async () => {
    const { user } = renderPage();

    const searchInput = screen.getByPlaceholderText(/Search security groups/i);
    await user.type(searchInput, 'web');

    expect(screen.getByRole('link', { name: 'sg-web' })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'sg-db' })).not.toBeInTheDocument();
  });

  it('opens create modal when Create button is clicked', async () => {
    const { user } = renderPage();

    await user.click(screen.getByRole('button', { name: /Create security group/i }));
    expect(screen.getByRole('heading', { name: 'Create security group' })).toBeInTheDocument();
  });
});
