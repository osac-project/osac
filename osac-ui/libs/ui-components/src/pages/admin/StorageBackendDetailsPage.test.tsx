import { Route, Routes } from 'react-router-dom';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { StorageBackend } from '@osac/types/private';
import { StorageBackendState } from '@osac/types/private';

import { StorageBackendDetailsPage } from './StorageBackendDetailsPage';
import type { MockTransportOverrides } from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

const mockBackend = {
  id: 'backend-1',
  metadata: { name: 'vast-prod' },
  spec: {
    provider: 'vast',
    endpoint: 'vast.example.com:443',
    description: 'Primary VAST cluster',
    credentials: { username: 'test-admin', password: 'test-secret' },
  },
  status: { state: StorageBackendState.READY, message: 'Reachable' },
} as StorageBackend;

const renderPage = (
  backends: StorageBackend[] = [mockBackend],
  transportOverrides?: MockTransportOverrides,
) =>
  renderWithProviders(
    <Routes>
      <Route
        path="/admin/infrastructure/storage/backends/:id"
        element={<StorageBackendDetailsPage />}
      />
      <Route
        path="/admin/infrastructure/storage/backends/:id/edit"
        element={<div>navigated-to-edit</div>}
      />
      <Route path="/admin/infrastructure/storage/backends" element={<div>navigated-to-list</div>} />
    </Routes>,
    {
      apiFixtures: { storageBackends: backends },
      routerEntries: ['/admin/infrastructure/storage/backends/backend-1'],
      transportOverrides,
    },
  );

describe('StorageBackendDetailsPage', () => {
  it('renders a loading state while the backend is being fetched', () => {
    renderPage([], {
      onStorageBackendGet: () => new Promise(() => undefined),
    });

    expect(screen.getByText('Loading resource title')).toBeInTheDocument();
  });

  it('renders an error state when the fetch fails', async () => {
    renderPage([], {
      onStorageBackendGet: () => {
        throw new ConnectError('backend service unavailable', Code.Unavailable);
      },
    });

    expect(await screen.findByText('Could not load storage backend')).toBeInTheDocument();
  });

  it('renders a not-found state when the backend does not exist', async () => {
    renderPage([]);

    expect(await screen.findByText('Storage backend not found')).toBeInTheDocument();
  });

  it('renders backend details and never renders credentials', async () => {
    renderPage();

    expect(await screen.findByRole('heading', { name: 'vast-prod' })).toBeInTheDocument();
    expect(screen.getByText('VAST Data')).toBeInTheDocument();
    expect(screen.getByText('Ownership mode')).toBeInTheDocument();
    expect(screen.getByText('OSAC-managed')).toBeInTheDocument();
    expect(screen.getByText('vast.example.com:443')).toBeInTheDocument();
    expect(screen.getByText('Primary VAST cluster')).toBeInTheDocument();
    expect(screen.getByText('Ready')).toBeInTheDocument();
    expect(screen.getByText('Reachable')).toBeInTheDocument();
    expect(screen.queryByText('test-admin')).not.toBeInTheDocument();
    expect(screen.queryByText('test-secret')).not.toBeInTheDocument();
  });

  it('identifies a NetApp backend as CSP-managed', async () => {
    renderPage([
      {
        ...mockBackend,
        spec: { ...mockBackend.spec, provider: 'netapp', cspManaged: true, endpoint: '' },
      } as StorageBackend,
    ]);

    expect(await screen.findByText('NetApp ONTAP')).toBeInTheDocument();
    expect(screen.getByText('CSP-managed')).toBeInTheDocument();
  });

  it('navigates to the edit route when Edit is clicked', async () => {
    const { user } = renderPage();

    await screen.findByRole('heading', { name: 'vast-prod' });
    await user.click(screen.getByRole('button', { name: /^Edit$/i }));

    expect(await screen.findByText('navigated-to-edit')).toBeInTheDocument();
  });

  it('navigates to the list after a successful delete', async () => {
    const { user } = renderPage();

    await screen.findByRole('heading', { name: 'vast-prod' });
    await user.click(screen.getByRole('button', { name: /^Delete$/i }));
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: /^Delete$/i }));

    expect(await screen.findByText('navigated-to-list')).toBeInTheDocument();
  });
});
