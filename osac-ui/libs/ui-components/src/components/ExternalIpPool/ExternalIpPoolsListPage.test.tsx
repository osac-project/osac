import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  type ExternalIPPool,
  ExternalIPPoolSchema,
  ExternalIPPoolState,
  IPFamily,
} from '@osac/types/private';
import { getViewTypePrefKey } from '@osac/ui-components/hooks/use-view-type';

import { ExternalIpPoolsListPage } from './ExternalIpPoolsListPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

const makePool = (
  id: string,
  name: string,
  ipFamily: IPFamily,
  cidrs: string[],
  state?: ExternalIPPoolState,
  capacity?: { total: bigint; available: bigint },
): ExternalIPPool =>
  create(ExternalIPPoolSchema, {
    id,
    metadata: { name },
    spec: { cidrs, ipFamily },
    status:
      state !== undefined
        ? {
            state,
            total: capacity?.total ?? 0n,
            allocated: 0n,
            available: capacity?.available ?? 0n,
            hub: '',
          }
        : undefined,
  });

const defaultPools = [
  makePool(
    'p-1',
    'prod-v4',
    IPFamily.IP_FAMILY_IPV4,
    ['192.168.1.0/24'],
    ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_READY,
    {
      total: 256n,
      available: 200n,
    },
  ),
  makePool(
    'p-2',
    'dev-v6',
    IPFamily.IP_FAMILY_IPV6,
    ['2001:db8::/64'],
    ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_PENDING,
  ),
];

const renderPage = (
  privateExternalIpPools: ExternalIPPool[] = defaultPools,
  routerEntries?: string[],
) =>
  renderWithProviders(<ExternalIpPoolsListPage />, {
    apiFixtures: { privateExternalIpPools },
    routerEntries,
  });

describe('ExternalIpPoolsListPage', () => {
  beforeEach(() => {
    localStorage.clear();
    mockNavigate.mockReset();
  });

  it('renders the page header', () => {
    renderPage();

    expect(screen.getByRole('heading', { name: 'External IP pools' })).toBeInTheDocument();
    expect(
      screen.getByText('Manage external IP address pools for this cloud platform.'),
    ).toBeInTheDocument();
  });

  it('renders column headers', async () => {
    localStorage.setItem(getViewTypePrefKey('external-ip-pools'), 'list');
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: 'Name' })).toBeInTheDocument();
    });
    expect(screen.getByRole('columnheader', { name: 'Status' })).toBeInTheDocument();
    expect(screen.queryByRole('columnheader', { name: 'IP family' })).not.toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'CIDRs' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Tenant' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Capacity' })).toBeInTheDocument();
  });

  it('renders a row per pool with name, CIDRs, and capacity', async () => {
    localStorage.setItem(getViewTypePrefKey('external-ip-pools'), 'list');
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('prod-v4')).toBeInTheDocument();
    });
    expect(screen.queryByText('IPv4')).not.toBeInTheDocument();
    expect(screen.getByText('192.168.1.0/24')).toBeInTheDocument();
    expect(screen.getByText('200 of 256 available')).toBeInTheDocument();
    expect(screen.getByText('dev-v6')).toBeInTheDocument();
    expect(screen.queryByText('IPv6')).not.toBeInTheDocument();
    expect(screen.getByText('2001:db8::/64')).toBeInTheDocument();
    expect(screen.getByText('2 external IP pools')).toBeInTheDocument();
  });

  it('shows a single CIDR inline and summarizes multiple CIDRs by count', async () => {
    localStorage.setItem(getViewTypePrefKey('external-ip-pools'), 'list');
    renderPage([
      makePool('p-1', 'prod-v4', IPFamily.IP_FAMILY_IPV4, ['192.168.1.0/24']),
      makePool('p-3', 'multi', IPFamily.IP_FAMILY_IPV4, [
        '192.168.1.0/24',
        '10.0.5.0/28',
        '172.16.0.0/24',
      ]),
    ]);

    await waitFor(() => {
      expect(screen.getByText('192.168.1.0/24')).toBeInTheDocument();
    });
    expect(screen.getByText('3 CIDRs')).toBeInTheDocument();
    expect(screen.queryByText('10.0.5.0/28')).not.toBeInTheDocument();
  });

  it('renders status labels for each pool', async () => {
    localStorage.setItem(getViewTypePrefKey('external-ip-pools'), 'list');
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('Ready')).toBeInTheDocument();
    });
    expect(screen.getByText('Provisioning')).toBeInTheDocument();
  });

  it('shows empty state when there are no pools', async () => {
    renderPage([]);

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: 'No external IP pools found', level: 2 }),
      ).toBeInTheDocument();
    });
    expect(screen.getByText('No external IP pools are available yet.')).toBeInTheDocument();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it('shows a filter-specific empty state when a search filter has no results', async () => {
    renderPage([], ['/admin/infrastructure/external-ip-pools?search=missing']);

    await waitFor(() => {
      expect(
        screen.getByRole('heading', {
          name: 'No external IP pools match your filters',
          level: 2,
        }),
      ).toBeInTheDocument();
    });
    expect(screen.getByText('Try a different state, or search term.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Clear all filters' })).toBeInTheDocument();
    expect(screen.queryByText('No external IP pools are available yet.')).not.toBeInTheDocument();
  });

  it('renders the search input', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByPlaceholderText('Search external IP pools')).toBeInTheDocument();
    });
  });

  it('renders the status filter toggle', async () => {
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

  it('links Create pool to the create route', () => {
    renderPage();

    expect(screen.getByRole('link', { name: 'Create pool' })).toHaveAttribute(
      'href',
      '/admin/infrastructure/external-ip-pools/create',
    );
  });

  it('navigates to the details route when View details is clicked', async () => {
    localStorage.setItem(getViewTypePrefKey('external-ip-pools'), 'list');
    const { user } = renderPage();

    await waitFor(() => {
      expect(screen.getByText('prod-v4')).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Actions for prod-v4' }));
    await user.click(screen.getByRole('menuitem', { name: 'View details' }));

    expect(mockNavigate).toHaveBeenCalledWith('/admin/infrastructure/external-ip-pools/p-1');
  });

  it('opens the delete confirmation dialog when Delete is clicked', async () => {
    localStorage.setItem(getViewTypePrefKey('external-ip-pools'), 'list');
    const { user } = renderPage();

    await waitFor(() => {
      expect(screen.getByText('prod-v4')).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Actions for prod-v4' }));
    await user.click(screen.getByText('Delete'));

    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(screen.getByText('Delete prod-v4?')).toBeInTheDocument();
  });

  it('removes the row from the table after a successful delete', async () => {
    localStorage.setItem(getViewTypePrefKey('external-ip-pools'), 'list');
    const { user } = renderPage();

    await waitFor(() => {
      expect(screen.getByText('prod-v4')).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Actions for prod-v4' }));
    await user.click(screen.getByText('Delete'));

    await user.click(screen.getByRole('button', { name: /^Delete$/i }));

    await waitFor(() => {
      expect(screen.queryByText('prod-v4')).not.toBeInTheDocument();
    });
    expect(screen.getByText('dev-v6')).toBeInTheDocument();
  });
});
