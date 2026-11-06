import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ExternalIPPoolSchema, ExternalIPPoolState, IPFamily } from '@osac/types/private';
import type { ExternalIPPool } from '@osac/types/private';

import { ExternalIpPoolDetailsPage } from './ExternalIpPoolDetailsPage';
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
  tenant = 'shared',
): ExternalIPPool =>
  create(ExternalIPPoolSchema, {
    id,
    metadata: {
      name,
      tenant,
      creationTimestamp: { seconds: BigInt(1700000000), nanos: 0 },
    },
    spec: { cidrs, ipFamily },
    status: {
      state: ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_READY,
      total: 256n,
      allocated: 56n,
      available: 200n,
    },
  });

const renderPage = (id: string, pools: ExternalIPPool[]) =>
  renderWithProviders(
    <Routes>
      <Route
        path="/admin/infrastructure/external-ip-pools/:id"
        element={<ExternalIpPoolDetailsPage />}
      />
    </Routes>,
    {
      apiFixtures: { privateExternalIpPools: pools },
      routerEntries: [`/admin/infrastructure/external-ip-pools/${id}`],
    },
  );

describe('ExternalIpPoolDetailsPage', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
  });

  it('renders pool details in overview, capacity, and CIDRs columns', async () => {
    renderPage('p-1', [makePool('p-1', 'prod-v4', IPFamily.IP_FAMILY_IPV4, ['192.168.1.0/24'])]);

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'prod-v4' })).toBeInTheDocument();
    });
    expect(screen.getByRole('heading', { name: 'Overview' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Capacity' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'CIDRs' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Assignment' })).not.toBeInTheDocument();
    expect(
      screen.queryByText('Routable address pool for tenant edge exposure.'),
    ).not.toBeInTheDocument();
    expect(screen.queryByText('Name')).not.toBeInTheDocument();
    expect(screen.getByLabelText('External IP pool overview').textContent).toMatch(
      /Status.*Created.*Tenant/s,
    );
    expect(screen.queryByText('IP family')).not.toBeInTheDocument();
    expect(screen.queryByText('IPv4')).not.toBeInTheDocument();
    expect(screen.getByText('Available')).toBeInTheDocument();
    expect(screen.getByText('200')).toBeInTheDocument();
    expect(screen.getByText('Total')).toBeInTheDocument();
    expect(screen.getByText('256')).toBeInTheDocument();
    expect(screen.queryByText('Allocated')).not.toBeInTheDocument();
    expect(screen.queryByText('Implementation strategy')).not.toBeInTheDocument();
    expect(screen.getByText('Ready')).toBeInTheDocument();
    expect(screen.getByText('Shared')).toBeInTheDocument();
    expect(screen.getByText('Shared').closest('.pf-v6-c-label')).toBeNull();
  });

  it('shows one CIDR and a more button when the pool has more than one CIDR', async () => {
    renderPage('p-3', [
      makePool('p-3', 'multi', IPFamily.IP_FAMILY_IPV4, [
        '192.168.1.0/24',
        '10.0.5.0/28',
        '172.16.0.0/24',
      ]),
    ]);

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'multi' })).toBeInTheDocument();
    });
    expect(screen.getByRole('heading', { name: 'CIDRs' })).toBeInTheDocument();
    expect(screen.getByText('192.168.1.0/24')).toBeInTheDocument();
    expect(screen.queryByText('10.0.5.0/28')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'More' })).toBeInTheDocument();
  });

  it('shows the tenant id when the pool is not in the shared tenant', async () => {
    renderPage('p-4', [
      makePool('p-4', 'tenant-pool', IPFamily.IP_FAMILY_IPV4, ['10.0.0.0/24'], 'acme'),
    ]);

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'tenant-pool' })).toBeInTheDocument();
    });
    expect(screen.getByText('acme')).toBeInTheDocument();
    expect(screen.queryByText('Shared')).not.toBeInTheDocument();
  });

  it('renders a not-found state when the pool does not exist', async () => {
    renderPage('missing', [
      makePool('p-1', 'prod-v4', IPFamily.IP_FAMILY_IPV4, ['192.168.1.0/24']),
    ]);

    await waitFor(() => {
      expect(screen.getByText('External IP pool not found')).toBeInTheDocument();
    });
  });

  it('offers a danger Delete button instead of an Actions menu', async () => {
    renderPage('p-1', [makePool('p-1', 'prod-v4', IPFamily.IP_FAMILY_IPV4, ['192.168.1.0/24'])]);

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'prod-v4' })).toBeInTheDocument();
    });

    expect(screen.queryByRole('button', { name: 'Actions' })).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Edit' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete' })).toHaveClass('pf-m-danger');
  });

  it('navigates to the list after a successful delete', async () => {
    const { user } = renderPage('p-1', [
      makePool('p-1', 'prod-v4', IPFamily.IP_FAMILY_IPV4, ['192.168.1.0/24']),
    ]);

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'prod-v4' })).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Delete' }));
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: /^Delete$/i }));

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith('/admin/infrastructure/external-ip-pools');
    });
  });
});
