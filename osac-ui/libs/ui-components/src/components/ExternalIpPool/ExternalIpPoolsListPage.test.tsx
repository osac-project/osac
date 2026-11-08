import { create } from '@bufbuild/protobuf';
import { screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  type ExternalIPPool,
  ExternalIPPoolSchema,
  ExternalIPPoolState,
  IPFamily,
  type NetworkClass,
  NetworkClassState,
} from '@osac/types/private';

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

const renderPage = (privateExternalIpPools: ExternalIPPool[] = defaultPools) =>
  renderWithProviders(<ExternalIpPoolsListPage />, { apiFixtures: { privateExternalIpPools } });

describe('ExternalIpPoolsListPage', () => {
  beforeEach(() => {
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
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: 'Name' })).toBeInTheDocument();
    });
    expect(screen.getByRole('columnheader', { name: 'Status' })).toBeInTheDocument();
    expect(screen.queryByRole('columnheader', { name: 'IP family' })).not.toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'IPv4 CIDR' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Available' })).toBeInTheDocument();
    // Available shows the total and the percentage, so there is no Total column.
    expect(screen.queryByRole('columnheader', { name: 'Total' })).not.toBeInTheDocument();
  });

  it('renders a row per pool with name, CIDRs, and availability', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('prod-v4')).toBeInTheDocument();
    });
    expect(screen.queryByText('IPv4')).not.toBeInTheDocument();
    expect(screen.getByText('192.168.1.0/24')).toBeInTheDocument();
    expect(screen.getByText('200 of 256 (78%)')).toBeInTheDocument();
    expect(screen.getByText('dev-v6')).toBeInTheDocument();
    expect(screen.queryByText('IPv6')).not.toBeInTheDocument();
    expect(screen.getByText('2001:db8::/64')).toBeInTheDocument();
  });

  it('lists pools past the first page', async () => {
    renderPage(
      Array.from({ length: 120 }, (_, index) =>
        makePool(`p-${index}`, `pool-${index}`, IPFamily.IP_FAMILY_IPV4, ['192.168.1.0/24']),
      ),
    );

    expect(await screen.findByText('pool-119')).toBeInTheDocument();
  });

  it('lists every CIDR of a pool', async () => {
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
    expect(screen.getByText('192.168.1.0/24, 10.0.5.0/28, 172.16.0.0/24')).toBeInTheDocument();
  });

  it('warns when a pool has no available addresses', async () => {
    renderPage([
      makePool(
        'p-1',
        'exhausted',
        IPFamily.IP_FAMILY_IPV4,
        ['192.168.1.0/28'],
        ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_READY,
        { total: 14n, available: 0n },
      ),
      ...defaultPools,
    ]);

    const row = (await screen.findByText('exhausted')).closest('tr') as HTMLElement;
    expect(within(row).getByText('None available')).toBeInTheDocument();
    expect(within(row).getByText('0 of 14 (0%)')).toBeInTheDocument();
    const otherRow = screen.getByText('prod-v4').closest('tr') as HTMLElement;
    expect(within(otherRow).queryByText('None available')).not.toBeInTheDocument();
  });

  it('never rounds a pool with some addresses left down to 0% or a pool with some used up to 100%', async () => {
    renderPage([
      makePool(
        'p-1',
        'almost-full',
        IPFamily.IP_FAMILY_IPV4,
        ['10.0.0.0/16'],
        ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_READY,
        { total: 65534n, available: 3n },
      ),
      makePool(
        'p-2',
        'almost-empty',
        IPFamily.IP_FAMILY_IPV4,
        ['10.1.0.0/16'],
        ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_READY,
        { total: 65534n, available: 65531n },
      ),
    ]);

    expect(await screen.findByText('3 of 65534 (<1%)')).toBeInTheDocument();
    expect(screen.getByText('65531 of 65534 (>99%)')).toBeInTheDocument();
  });

  it('shows the status message in a popover on the status label', async () => {
    const failed = create(ExternalIPPoolSchema, {
      id: 'p-9',
      metadata: { name: 'broken' },
      spec: { cidrs: ['192.168.9.0/24'], ipFamily: IPFamily.IP_FAMILY_IPV4 },
      status: {
        state: ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_FAILED,
        message: 'Range overlaps another pool',
        total: 0n,
        allocated: 0n,
        available: 0n,
        hub: '',
      },
    });
    const { user } = renderPage([failed]);

    await user.click(await screen.findByRole('button', { name: 'Failed' }));
    expect(await screen.findByText('Range overlaps another pool')).toBeInTheDocument();
  });

  it('renders status labels for each pool', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('Ready')).toBeInTheDocument();
    });
    expect(screen.getByText('Provisioning')).toBeInTheDocument();
  });

  it('shows empty state when there are no pools', async () => {
    renderPage([]);

    expect(
      await screen.findByRole('heading', { name: 'No external IP pools' }),
    ).toBeInTheDocument();
    expect(screen.getAllByRole('link', { name: 'Create external IP pool' })).toHaveLength(2);
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it('links Create external IP pool to the create route', () => {
    renderPage();

    expect(screen.getByRole('link', { name: 'Create external IP pool' })).toHaveAttribute(
      'href',
      '/admin/infrastructure/external-ip-pools/create',
    );
  });

  it('navigates to the details route when View details is clicked', async () => {
    const { user } = renderPage();

    await waitFor(() => {
      expect(screen.getByText('prod-v4')).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Actions for prod-v4' }));
    await user.click(screen.getByRole('menuitem', { name: 'View details' }));

    expect(mockNavigate).toHaveBeenCalledWith('/admin/infrastructure/external-ip-pools/p-1');
  });

  it('opens the delete confirmation dialog when Delete is clicked', async () => {
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

  it('disables Delete on a pool with allocated addresses', async () => {
    const pool = create(ExternalIPPoolSchema, {
      id: 'p-used',
      metadata: { name: 'used-pool' },
      spec: { cidrs: ['198.51.100.0/24'], ipFamily: IPFamily.IP_FAMILY_IPV4 },
      status: {
        state: ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_READY,
        total: 254n,
        allocated: 1n,
        available: 253n,
      },
    });
    const { user } = renderPage([pool]);

    await user.click(await screen.findByRole('button', { name: 'Actions for used-pool' }));
    const deleteItem = screen.getByRole('menuitem', { name: 'Delete' });
    expect(deleteItem).toHaveAttribute('aria-disabled', 'true');
    await user.hover(deleteItem);
    expect(await screen.findByText('Delete its external IP first.')).toBeInTheDocument();
    await user.click(deleteItem);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('disables Create until the network class is Ready', async () => {
    const { user } = renderWithProviders(<ExternalIpPoolsListPage />, {
      apiFixtures: {
        privateExternalIpPools: defaultPools,
        networkClasses: [
          { id: 'nc-1', status: { state: NetworkClassState.PENDING } } as unknown as NetworkClass,
        ],
      },
    });

    await waitFor(() =>
      expect(screen.getByRole('link', { name: 'Create external IP pool' })).toHaveAttribute(
        'aria-disabled',
        'true',
      ),
    );
    const createButton = screen.getByRole('link', { name: 'Create external IP pool' });
    await user.hover(createButton);
    expect(
      await screen.findByText('Available when the network class is Ready.'),
    ).toBeInTheDocument();
  });

  it('enables Create when the network class is Ready', async () => {
    renderWithProviders(<ExternalIpPoolsListPage />, {
      apiFixtures: {
        privateExternalIpPools: defaultPools,
        networkClasses: [
          { id: 'nc-1', status: { state: NetworkClassState.READY } } as unknown as NetworkClass,
        ],
      },
    });

    const createButton = await screen.findByRole('link', { name: 'Create external IP pool' });
    await waitFor(() => expect(createButton).not.toHaveAttribute('aria-disabled', 'true'));
  });
});
