import { Route, Routes } from 'react-router-dom';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { StorageBackend, StorageTier } from '@osac/types/private';
import { StorageBackendState, StorageProtocol, StorageTierState } from '@osac/types/private';

import { StorageTierDetailsPage } from './StorageTierDetailsPage';
import type { MockApiFixtures } from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

const backendA: StorageBackend = {
  id: 'backend-a',
  metadata: { name: 'Fast NVMe' },
  spec: { provider: 'vast', endpoint: 'backend-a.example.com', credentials: {} },
  status: { state: StorageBackendState.READY },
} as StorageBackend;

const tier: StorageTier = {
  id: 'tier-1',
  metadata: { name: 'fast' },
  spec: {
    description: 'Fast tier for latency-sensitive workloads',
    protocol: StorageProtocol.NFS,
    backends: [
      {
        backendId: 'backend-a',
        maxReadBandwidthMbs: 90,
        maxWriteBandwidthMbs: 70,
        encryptionEnabled: false,
      },
    ],
  },
  status: { state: StorageTierState.ACTIVE },
} as StorageTier;

const renderAt = (path: string, fixtures?: MockApiFixtures) =>
  renderWithProviders(
    <Routes>
      <Route path="/admin/infrastructure/storage/tiers/:id" element={<StorageTierDetailsPage />} />
      <Route path="/admin/infrastructure/storage/tiers" element={<div>navigated-to-list</div>} />
      <Route
        path="/admin/infrastructure/storage/tiers/:id/edit"
        element={<div>navigated-to-edit</div>}
      />
    </Routes>,
    { routerEntries: [path], apiFixtures: fixtures },
  );

describe('StorageTierDetailsPage', () => {
  it('shows the loading state while the tier is fetching', () => {
    renderWithProviders(
      <Routes>
        <Route
          path="/admin/infrastructure/storage/tiers/:id"
          element={<StorageTierDetailsPage />}
        />
      </Routes>,
      {
        routerEntries: ['/admin/infrastructure/storage/tiers/tier-1'],
        transportOverrides: {
          // Never resolves — keeps the query in isLoading state for this assertion.
          onStorageTierGet: () => new Promise(() => undefined),
        },
      },
    );

    expect(screen.getByText('Loading resource title')).toBeInTheDocument();
  });

  it('renders name, description, state, and backend associations', async () => {
    renderAt('/admin/infrastructure/storage/tiers/tier-1', {
      storageTiers: [tier],
      storageBackends: [backendA],
    });

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'fast' })).toBeInTheDocument();
    });
    expect(screen.getByRole('link', { name: 'Storage tiers' })).toHaveAttribute(
      'href',
      '/admin/infrastructure/storage/tiers',
    );
    expect(screen.getByText('Fast tier for latency-sensitive workloads')).toBeInTheDocument();
    expect(screen.getAllByText('Active')).not.toHaveLength(0);
    await waitFor(() => {
      expect(screen.getByText('Fast NVMe')).toBeInTheDocument();
    });
    expect(screen.getByText('NFS')).toBeInTheDocument();
    expect(screen.getByText('90 MB/s')).toBeInTheDocument();
    expect(screen.getByText('70 MB/s')).toBeInTheDocument();
    expect(screen.getByText('No')).toBeInTheDocument();
  });

  it('shows the status message when present', async () => {
    renderAt('/admin/infrastructure/storage/tiers/tier-1', {
      storageTiers: [
        { ...tier, status: { ...tier.status, message: 'Degraded backend' } } as StorageTier,
      ],
      storageBackends: [backendA],
    });

    await waitFor(() => {
      expect(screen.getByText('Degraded backend')).toBeInTheDocument();
    });
  });

  it('shows a dash for the description field when the tier has none', async () => {
    renderAt('/admin/infrastructure/storage/tiers/tier-1', {
      storageTiers: [{ ...tier, spec: { ...tier.spec, description: '' } } as StorageTier],
      storageBackends: [backendA],
    });

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'fast' })).toBeInTheDocument();
    });
    expect(screen.getByText('Description')).toBeInTheDocument();
    expect(screen.getByText('—')).toBeInTheDocument();
  });

  it('falls back to the raw backend id when resolution fails', async () => {
    renderAt('/admin/infrastructure/storage/tiers/tier-1', {
      storageTiers: [tier],
      storageBackends: [],
    });

    await waitFor(() => {
      expect(screen.getByText('backend-a')).toBeInTheDocument();
    });
  });

  it('shows the backend-fetch error verbatim when the backend-name lookup fails, without breaking the id fallback', async () => {
    renderWithProviders(
      <Routes>
        <Route
          path="/admin/infrastructure/storage/tiers/:id"
          element={<StorageTierDetailsPage />}
        />
      </Routes>,
      {
        routerEntries: ['/admin/infrastructure/storage/tiers/tier-1'],
        apiFixtures: { storageTiers: [tier] },
        transportOverrides: {
          onStorageBackendList: () => {
            throw new ConnectError('backend service unavailable', Code.Unavailable);
          },
        },
      },
    );

    await waitFor(() => {
      expect(screen.getByText('Failed to fetch storage backends')).toBeInTheDocument();
    });
    expect(screen.getByText('backend service unavailable')).toBeInTheDocument();
    expect(screen.getByText('backend-a')).toBeInTheDocument();
  });

  it('renders an error state when the tier fails to load', async () => {
    renderWithProviders(
      <Routes>
        <Route
          path="/admin/infrastructure/storage/tiers/:id"
          element={<StorageTierDetailsPage />}
        />
        <Route path="/admin/infrastructure/storage/tiers" element={<div>navigated-to-list</div>} />
      </Routes>,
      {
        routerEntries: ['/admin/infrastructure/storage/tiers/tier-1'],
        transportOverrides: {
          onStorageTierGet: () => {
            throw new ConnectError('storage tier service unavailable', Code.Unavailable);
          },
        },
      },
    );

    await waitFor(() => {
      expect(screen.getByText('Could not load storage tier')).toBeInTheDocument();
    });
  });

  it('renders a not-found state and returns to the list for an unknown id', async () => {
    const { user } = renderAt('/admin/infrastructure/storage/tiers/unknown-id', {
      storageTiers: [tier],
      storageBackends: [backendA],
    });

    await waitFor(() => {
      expect(screen.getByText('Storage tier not found')).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: /Return to storage tiers/i }));

    await waitFor(() => {
      expect(screen.getByText('navigated-to-list')).toBeInTheDocument();
    });
  });

  it('navigates to the edit route when Edit is clicked', async () => {
    const { user } = renderAt('/admin/infrastructure/storage/tiers/tier-1', {
      storageTiers: [tier],
      storageBackends: [backendA],
    });

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'fast' })).toBeInTheDocument();
    });
    await user.click(screen.getByRole('button', { name: 'Edit' }));

    await waitFor(() => {
      expect(screen.getByText('navigated-to-edit')).toBeInTheDocument();
    });
  });

  it('deletes the tier and navigates to the list on success', async () => {
    const { user } = renderAt('/admin/infrastructure/storage/tiers/tier-1', {
      storageTiers: [tier],
      storageBackends: [backendA],
    });

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'fast' })).toBeInTheDocument();
    });
    await user.click(screen.getByRole('button', { name: 'Delete' }));
    await user.click(screen.getByRole('button', { name: /^Delete$/i }));

    await waitFor(() => {
      expect(screen.getByText('navigated-to-list')).toBeInTheDocument();
    });
  });

  it('shows a referential-integrity delete error without navigating away', async () => {
    const { user } = renderWithProviders(
      <Routes>
        <Route
          path="/admin/infrastructure/storage/tiers/:id"
          element={<StorageTierDetailsPage />}
        />
        <Route path="/admin/infrastructure/storage/tiers" element={<div>navigated-to-list</div>} />
      </Routes>,
      {
        routerEntries: ['/admin/infrastructure/storage/tiers/tier-1'],
        apiFixtures: { storageTiers: [tier], storageBackends: [backendA] },
        transportOverrides: {
          onStorageTierDelete: () => {
            throw new ConnectError(
              'Storage tier is referenced by a Tenant',
              Code.FailedPrecondition,
            );
          },
        },
      },
    );

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'fast' })).toBeInTheDocument();
    });
    await user.click(screen.getByRole('button', { name: 'Delete' }));
    await user.click(screen.getByRole('button', { name: /^Delete$/i }));

    await waitFor(() => {
      expect(screen.getByText('Storage tier is referenced by a Tenant')).toBeInTheDocument();
    });
    expect(screen.queryByText('navigated-to-list')).not.toBeInTheDocument();
  });
});
