import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { StorageTier } from '@osac/types/private';
import { StorageBackendSchema } from '@osac/types/private';
import { StorageProtocol, StorageTierState } from '@osac/types/private';
import {
  type RenderWithProvidersOptions,
  renderWithProviders,
} from '@osac/ui-components/test-utils/TestProviders';

import { StorageRoutes } from './StorageRoutes';

vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    // useBlocker requires a data router; this test harness renders under a
    // plain MemoryRouter, so StorageBackendCreatePage's LeaveFormConfirmation
    // is stubbed out here rather than exercised (see its own test file).
    useBlocker: () => ({ state: 'unblocked' as const }),
  };
});

const renderAt = (path: string, options: RenderWithProvidersOptions = {}) =>
  renderWithProviders(
    <Routes>
      <Route path="/admin/infrastructure/storage/*" element={<StorageRoutes />} />
    </Routes>,
    { routerEntries: [path], ...options },
  );

describe('StorageRoutes', () => {
  it('redirects the bare /admin/infrastructure/storage path to the Backends tab', () => {
    renderAt('/admin/infrastructure/storage');

    expect(screen.getByRole('link', { name: 'Create backend' })).toBeInTheDocument();
  });

  it('renders the real create form for backends/create', () => {
    renderAt('/admin/infrastructure/storage/backends/create');

    expect(screen.getByRole('heading', { name: 'Create storage backend' })).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Name' })).toBeInTheDocument();
  });

  it('renders the real edit form for backends/:id/edit', async () => {
    renderAt('/admin/infrastructure/storage/backends/abc-123/edit', {
      apiFixtures: {
        storageBackends: [
          create(StorageBackendSchema, {
            id: 'abc-123',
            metadata: { name: 'vast-prod-1' },
            spec: { provider: 'vast', endpoint: 'vast.example.com:443', description: '' },
          }),
        ],
      },
    });

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Edit storage backend' })).toBeInTheDocument();
    });
    expect(screen.getByRole('textbox', { name: 'Endpoint' })).toHaveValue('vast.example.com:443');
    expect(screen.queryByText('This feature is coming soon.')).not.toBeInTheDocument();
  });

  it('renders the details page for backends/:id', async () => {
    renderAt('/admin/infrastructure/storage/backends/abc-123', {
      apiFixtures: {
        storageBackends: [
          create(StorageBackendSchema, {
            id: 'abc-123',
            metadata: { name: 'vast-prod-1' },
            spec: { provider: 'vast', endpoint: 'vast.example.com:443', description: '' },
          }),
        ],
      },
    });

    expect(await screen.findByRole('heading', { name: 'vast-prod-1' })).toBeInTheDocument();
  });

  it('renders the Tiers tab at /admin/infrastructure/storage/tiers', async () => {
    renderAt('/admin/infrastructure/storage/tiers');

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'Create tier' })).toBeInTheDocument();
    });
  });

  it('renders the create storage tier page for tiers/create', () => {
    renderAt('/admin/infrastructure/storage/tiers/create');

    expect(screen.getByRole('heading', { name: 'Create storage tier' })).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Name' })).toBeInTheDocument();
    expect(screen.queryByText('This feature is coming soon.')).not.toBeInTheDocument();
  });

  it('renders the real details page for tiers/:id', async () => {
    const tier = {
      id: 'tier-123',
      metadata: { name: 'fast-tier', version: 1 },
      spec: {
        description: '',
        protocol: StorageProtocol.NFS,
        backends: [
          {
            backendId: 'backend-1',
            maxReadBandwidthMbs: 100,
            maxWriteBandwidthMbs: 100,
            encryptionEnabled: false,
          },
        ],
      },
      status: { state: StorageTierState.ACTIVE },
    } as StorageTier;

    renderAt('/admin/infrastructure/storage/tiers/tier-123', {
      apiFixtures: { storageTiers: [tier] },
    });

    expect(await screen.findByRole('heading', { name: 'fast-tier' })).toBeInTheDocument();
  });

  it('renders the real edit form for tiers/:id/edit', async () => {
    const tier = {
      id: 'tier-123',
      metadata: { name: 'fast-tier', version: 1 },
      spec: {
        description: '',
        protocol: StorageProtocol.NFS,
        backends: [
          {
            backendId: 'backend-1',
            maxReadBandwidthMbs: 100,
            maxWriteBandwidthMbs: 100,
            encryptionEnabled: false,
          },
        ],
      },
      status: { state: StorageTierState.ACTIVE },
    } as StorageTier;

    renderAt('/admin/infrastructure/storage/tiers/tier-123/edit', {
      apiFixtures: { storageTiers: [tier] },
    });

    expect(await screen.findByRole('heading', { name: 'Edit storage tier' })).toBeInTheDocument();
    expect(await screen.findByRole('textbox', { name: 'Name' })).toBeDisabled();
  });
});
