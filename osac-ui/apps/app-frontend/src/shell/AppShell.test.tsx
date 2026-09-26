import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { SessionProvider } from '@osac/ui-components/hooks/use-session';
import type { UserRole } from '@osac/ui-components/shellTypes';
import { renderWithProviders } from '@osac/ui-components/test-utils/TestProviders';

vi.mock('./StorageRoutes', () => ({
  StorageRoutes: () => <h1>Storage routes</h1>,
}));

vi.mock('react-svg', () => ({
  ReactSVG: () => <svg aria-hidden="true" />,
}));

import { AppShell } from './AppShell';

const renderAppShell = (entry: string, role: UserRole = 'admin') =>
  renderWithProviders(
    <SessionProvider role={role} username="test-user" tenantId="tenant-1">
      <AppShell logout={vi.fn().mockResolvedValue(undefined)} />
    </SessionProvider>,
    {
      apiFixtures: { privateInstanceTypes: [], privateBaremetalInstanceTypes: [] },
      routerEntries: [entry],
    },
  );

describe('AppShell', () => {
  it('renders the storage route through the admin shell', async () => {
    renderAppShell('/admin/infrastructure/storage/backends');

    expect(await screen.findByRole('heading', { name: 'Storage routes' })).toBeInTheDocument();
  });

  it('renders the instance type list route through the admin shell', async () => {
    renderAppShell('/admin/infrastructure/instance-types');

    expect(await screen.findByRole('heading', { name: 'Instance types' })).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByText('No instance types yet.')).toBeInTheDocument();
    });
  });

  it('renders the instance type create shell through the admin shell', async () => {
    renderAppShell('/admin/infrastructure/instance-types/create');

    expect(
      await screen.findByRole('heading', { name: 'Create instance type' }),
    ).toBeInTheDocument();
  });

  it('renders the bare metal instance type list route through the admin shell', async () => {
    renderAppShell('/admin/infrastructure/baremetal-instance-types');

    expect(
      await screen.findByRole('heading', { name: 'Bare metal instance types' }),
    ).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByText('No bare metal instance types yet.')).toBeInTheDocument();
    });
  });

  it('does not render VM routes for admin — falls through to default', async () => {
    renderAppShell('/vms', 'admin');

    expect(screen.queryByRole('heading', { name: /virtual machines/i })).toBeNull();
    expect(await screen.findByRole('heading', { name: 'Tenants' })).toBeInTheDocument();
  });

  it('does not render cluster routes for admin — falls through to default', async () => {
    renderAppShell('/clusters', 'admin');

    expect(screen.queryByRole('heading', { name: /clusters/i })).toBeNull();
    expect(await screen.findByRole('heading', { name: 'Tenants' })).toBeInTheDocument();
  });

  it('does not render bare metal routes for admin — falls through to default', async () => {
    renderAppShell('/bare-metal', 'admin');

    expect(screen.queryByRole('heading', { name: /bare metal/i })).toBeNull();
    expect(await screen.findByRole('heading', { name: 'Tenants' })).toBeInTheDocument();
  });
});
