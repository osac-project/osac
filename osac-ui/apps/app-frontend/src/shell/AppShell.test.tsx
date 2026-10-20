import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { Clusters, ServiceTier } from '@osac/types';
import { SessionProvider } from '@osac/ui-components/hooks/use-session';
import type { UserRole } from '@osac/ui-components/shellTypes';
import { createMockConnectTransport } from '@osac/ui-components/test-utils/createMockConnectTransport';
import { renderWithProviders } from '@osac/ui-components/test-utils/TestProviders';

vi.mock('./StorageRoutes', () => ({
  StorageRoutes: () => <h1>Storage routes</h1>,
}));

vi.mock('react-svg', () => ({
  ReactSVG: () => <svg aria-hidden="true" />,
}));

import { AppShell } from './AppShell';

const renderAppShell = (
  entry: string,
  role: UserRole = 'admin',
  enabledServices: ServiceTier[] = [ServiceTier.CAAS, ServiceTier.VMAAS, ServiceTier.BMAAS],
  transport?: ReturnType<typeof createMockConnectTransport>,
) =>
  renderWithProviders(
    <SessionProvider role={role} username="test-user" tenantId="tenant-1">
      <AppShell logout={vi.fn().mockResolvedValue(undefined)} />
    </SessionProvider>,
    {
      apiFixtures: {
        enabledServices,
        privateInstanceTypes: [],
        privateBaremetalInstanceTypes: [],
      },
      routerEntries: [entry],
      transport,
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

  it.each(['/clusters', '/clusters/cluster-1'])(
    'hides cluster navigation and makes no cluster RPC for admin at %s',
    async (path) => {
      const enabledServices = [ServiceTier.CAAS, ServiceTier.VMAAS, ServiceTier.BMAAS];
      const transport = createMockConnectTransport({ enabledServices });
      const unary = vi.spyOn(transport, 'unary');

      renderAppShell(path, 'admin', enabledServices, transport);

      expect(await screen.findByRole('heading', { name: 'Tenants' })).toBeInTheDocument();
      const navigation = screen.getByRole('navigation', {
        name: 'Primary navigation',
        hidden: true,
      });
      expect(within(navigation).queryByText('Clusters')).not.toBeInTheDocument();
      expect(screen.queryByRole('heading', { name: /clusters/i })).not.toBeInTheDocument();
      expect(
        unary.mock.calls.filter(([method]) => method.parent.typeName === Clusters.typeName),
      ).toHaveLength(0);
    },
  );

  it('queries the public cluster service for an allowed tenant route', async () => {
    const enabledServices = [ServiceTier.CAAS];
    const transport = createMockConnectTransport({ enabledServices, clusters: [] });
    const unary = vi.spyOn(transport, 'unary');

    renderAppShell('/clusters', 'tenant-user', enabledServices, transport);

    expect(await screen.findByRole('heading', { name: 'Clusters' })).toBeInTheDocument();
    const navigation = screen.getByRole('navigation', { name: 'Primary navigation', hidden: true });
    expect(within(navigation).getByText('Clusters')).toBeInTheDocument();
    await waitFor(() => {
      expect(
        unary.mock.calls.filter(([method]) => method.parent.typeName === Clusters.typeName),
      ).toHaveLength(1);
    });
  });

  it('does not render bare metal routes for admin — falls through to default', async () => {
    renderAppShell('/bare-metal', 'admin');

    expect(screen.queryByRole('heading', { name: /bare metal/i })).toBeNull();
    expect(await screen.findByRole('heading', { name: 'Tenants' })).toBeInTheDocument();
  });

  it('does not render volume routes for admin — falls through to default', async () => {
    renderAppShell('/storage/volumes', 'admin');

    expect(screen.queryByRole('heading', { name: /volumes/i })).toBeNull();
    expect(await screen.findByRole('heading', { name: 'Tenants' })).toBeInTheDocument();
  });

  it('does not render volume routes for tenant-user — falls through to default', async () => {
    renderAppShell('/storage/volumes', 'tenant-user');

    expect(screen.queryByRole('heading', { name: /volumes/i })).toBeNull();
    expect(await screen.findByRole('heading', { name: 'Catalog' })).toBeInTheDocument();
  });

  it.each([
    {
      path: '/vms/vm-1',
      service: ServiceTier.VMAAS,
      label: 'Virtual Machine',
    },
    {
      path: '/clusters/cluster-1',
      service: ServiceTier.CAAS,
      label: 'Cluster',
    },
    {
      path: '/bare-metal/instance-1',
      service: ServiceTier.BMAAS,
      label: 'Bare Metal',
    },
  ])(
    'shows a service-unavailable page for a disabled $label route',
    async ({ path, service, label }) => {
      renderAppShell(
        path,
        'tenant-user',
        [ServiceTier.CAAS, ServiceTier.VMAAS, ServiceTier.BMAAS].filter(
          (enabledService) => enabledService !== service,
        ),
      );

      expect(
        await screen.findByRole('heading', { name: 'Service unavailable' }),
      ).toBeInTheDocument();
      expect(
        screen.getByText(`The ${label} service is not enabled on this server.`),
      ).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Go to catalog' })).toBeInTheDocument();
    },
  );
});
