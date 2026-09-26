import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { Tenant } from '@osac/types/private';
import { TenantState } from '@osac/types/private';

import TenantListPage from './TenantListPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

const makeTenant = (id: string, name: string, state?: TenantState) =>
  ({
    id,
    metadata: {
      name,
      creationTimestamp: { seconds: BigInt(1717000000), nanos: 0 },
    },
    spec: { domains: [`${name}.example.com`] },
    status: state !== undefined ? { state } : undefined,
  }) as Tenant;

const defaultTenants = [
  makeTenant('t-1', 'acme', TenantState.SYNCED),
  makeTenant('t-2', 'globex', TenantState.PENDING),
];

const renderPage = (tenants: Tenant[] = defaultTenants) =>
  renderWithProviders(<TenantListPage />, { apiFixtures: { tenants } });

describe('TenantListPage', () => {
  it('renders the page title', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Tenants' })).toBeInTheDocument();
    });
    expect(screen.getByRole('link', { name: 'Create tenant' })).toHaveAttribute(
      'href',
      '/admin/tenants/create',
    );
  });

  it('renders tenant rows with names', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('acme')).toBeInTheDocument();
    });
    expect(screen.getByText('globex')).toBeInTheDocument();
  });

  it('renders status labels for each tenant', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('Synced')).toBeInTheDocument();
    });
    expect(screen.getByText('Pending')).toBeInTheDocument();
  });

  it('renders primary domain for each tenant', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('acme.example.com')).toBeInTheDocument();
    });
    expect(screen.getByText('globex.example.com')).toBeInTheDocument();
  });

  it('shows empty state when there are no tenants', async () => {
    renderPage([]);

    await waitFor(() => {
      expect(screen.getByText('No tenants yet. Register one to get started.')).toBeInTheDocument();
    });
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it('shows filtered empty state when search matches nothing', async () => {
    const { user } = renderPage();

    await waitFor(() => {
      expect(screen.getByText('acme')).toBeInTheDocument();
    });

    const searchInput = screen.getByRole('textbox', { name: 'Search tenants' });
    await user.type(searchInput, 'nonexistent');

    expect(screen.getByText('No tenants match your search.')).toBeInTheDocument();
    expect(screen.queryByText('acme')).not.toBeInTheDocument();
  });

  it('filters tenants by name', async () => {
    const { user } = renderPage();

    await waitFor(() => {
      expect(screen.getByText('acme')).toBeInTheDocument();
    });

    const searchInput = screen.getByRole('textbox', { name: 'Search tenants' });
    await user.type(searchInput, 'acme');

    expect(screen.getByText('acme')).toBeInTheDocument();
    expect(screen.queryByText('globex')).not.toBeInTheDocument();
  });
});
