import { Route, Routes } from 'react-router-dom';
import { screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { StorageBackend } from '@osac/types/private';
import { StorageBackendState } from '@osac/types/private';

import { StorageBackendDetailsPage } from './StorageBackendDetailsPage';
import * as storageBackendsApi from '../../api/v1/private/storage-backends';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.mock('../../api/v1/private/storage-backends', async (importOriginal) => {
  const actual = await importOriginal<typeof storageBackendsApi>();
  return {
    ...actual,
    usePrivateStorageBackend: vi.fn(),
  };
});

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

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

const mockUsePrivateStorageBackend = (
  overrides: Partial<ReturnType<typeof storageBackendsApi.usePrivateStorageBackend>>,
) =>
  vi.mocked(storageBackendsApi.usePrivateStorageBackend).mockReturnValue({
    data: undefined,
    isLoading: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
    ...overrides,
  } as unknown as ReturnType<typeof storageBackendsApi.usePrivateStorageBackend>);

const renderPage = () =>
  renderWithProviders(
    <Routes>
      <Route
        path="/admin/infrastructure/storage/backends/:id"
        element={<StorageBackendDetailsPage />}
      />
    </Routes>,
    { routerEntries: ['/admin/infrastructure/storage/backends/backend-1'] },
  );

describe('StorageBackendDetailsPage', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
  });

  it('renders a loading state while the backend is being fetched', () => {
    mockUsePrivateStorageBackend({ isLoading: true });

    renderPage();

    expect(screen.getByText('Storage Backends')).toBeInTheDocument();
  });

  it('renders an error state when the fetch fails', () => {
    mockUsePrivateStorageBackend({ isError: true, error: new Error('boom') });

    renderPage();

    expect(screen.getByText('Could not load storage backend')).toBeInTheDocument();
  });

  it('renders a not-found state when the backend does not exist', () => {
    mockUsePrivateStorageBackend({});

    renderPage();

    expect(screen.getByText('Storage backend not found')).toBeInTheDocument();
  });

  it('renders backend details and never renders credentials', () => {
    mockUsePrivateStorageBackend({ data: mockBackend });

    renderPage();

    expect(screen.getByRole('heading', { name: 'vast-prod' })).toBeInTheDocument();
    expect(screen.getByText('vast')).toBeInTheDocument();
    expect(screen.getByText('Ownership mode')).toBeInTheDocument();
    expect(screen.getByText('OSAC-managed')).toBeInTheDocument();
    expect(screen.getByText('vast.example.com:443')).toBeInTheDocument();
    expect(screen.getByText('Primary VAST cluster')).toBeInTheDocument();
    expect(screen.getByText('Ready')).toBeInTheDocument();
    expect(screen.getByText('Reachable')).toBeInTheDocument();
    expect(screen.queryByText('test-admin')).not.toBeInTheDocument();
    expect(screen.queryByText('test-secret')).not.toBeInTheDocument();
  });

  it('identifies a NetApp backend as CSP-managed', () => {
    mockUsePrivateStorageBackend({
      data: {
        ...mockBackend,
        spec: { ...mockBackend.spec, provider: 'netapp', cspManaged: true, endpoint: '' },
      } as StorageBackend,
    });

    renderPage();

    expect(screen.getByText('netapp')).toBeInTheDocument();
    expect(screen.getByText('CSP-managed')).toBeInTheDocument();
  });

  it('navigates to the edit route when Edit is clicked', async () => {
    mockUsePrivateStorageBackend({ data: mockBackend });

    const { user } = renderPage();

    await user.click(screen.getByRole('button', { name: /^Edit$/i }));

    expect(mockNavigate).toHaveBeenCalledWith(
      '/admin/infrastructure/storage/backends/backend-1/edit',
    );
  });

  it('navigates to the list after a successful delete', async () => {
    mockUsePrivateStorageBackend({ data: mockBackend });

    const { user } = renderPage();

    await user.click(screen.getByRole('button', { name: /^Delete$/i }));
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: /^Delete$/i }));

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith('/admin/infrastructure/storage/backends');
    });
  });
});
