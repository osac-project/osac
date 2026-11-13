import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { StorageBackend } from '@osac/types/private';
import { StorageBackendState } from '@osac/types/private';
import { getViewTypePrefKey } from '@osac/ui-components/hooks/use-view-type';

import { StorageBackendsListPage } from './StorageBackendsListPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

const makeBackend = (
  id: string,
  name: string,
  provider: string,
  endpoint: string,
  state?: StorageBackendState,
) =>
  ({
    id,
    metadata: { name },
    spec: { provider, endpoint },
    status: state !== undefined ? { state } : undefined,
  }) as StorageBackend;

const defaultBackends = [
  makeBackend('b-1', 'vast-prod', 'vast', 'vast.example.com', StorageBackendState.READY),
  makeBackend('b-2', 'ceph-dev', 'ceph', 'ceph.example.com', StorageBackendState.UNSPECIFIED),
];

const renderPage = (
  storageBackends: StorageBackend[] = defaultBackends,
  routerEntries?: string[],
) =>
  renderWithProviders(<StorageBackendsListPage />, {
    apiFixtures: { storageBackends },
    routerEntries,
  });

describe('StorageBackendsListPage', () => {
  beforeEach(() => {
    localStorage.clear();
    mockNavigate.mockReset();
  });

  it('renders column headers', async () => {
    localStorage.setItem(getViewTypePrefKey('storage-backends'), 'list');
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: 'Name' })).toBeInTheDocument();
    });
    expect(screen.getByRole('columnheader', { name: 'Provider' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Endpoint' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Status' })).toBeInTheDocument();
  });

  it('renders a row per backend with name, provider, and endpoint', async () => {
    localStorage.setItem(getViewTypePrefKey('storage-backends'), 'list');
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('vast-prod')).toBeInTheDocument();
    });
    expect(screen.getByText('VAST Data')).toBeInTheDocument();
    expect(screen.getByText('vast.example.com')).toBeInTheDocument();
    expect(screen.getByText('ceph-dev')).toBeInTheDocument();
    expect(screen.getByText('ceph')).toBeInTheDocument();
    expect(screen.getByText('2 storage backends')).toBeInTheDocument();
  });

  it('links the backend name to its details page', async () => {
    localStorage.setItem(getViewTypePrefKey('storage-backends'), 'list');
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'vast-prod' })).toBeInTheDocument();
    });
    expect(screen.getByRole('link', { name: 'vast-prod' })).toHaveAttribute(
      'href',
      '/admin/infrastructure/storage/backends/b-1',
    );
  });

  it('renders status labels for each backend', async () => {
    localStorage.setItem(getViewTypePrefKey('storage-backends'), 'list');
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('Ready')).toBeInTheDocument();
    });
    expect(screen.getByText('Unspecified')).toBeInTheDocument();
  });

  it('shows empty state when there are no backends', async () => {
    renderPage([]);

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: 'No storage backends found', level: 2 }),
      ).toBeInTheDocument();
    });
    expect(screen.getByText('No storage backends are available yet.')).toBeInTheDocument();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it('shows a filter-specific empty state when a search filter has no results', async () => {
    renderPage(defaultBackends, ['/admin/infrastructure/storage/backends?search=missing']);

    await waitFor(() => {
      expect(
        screen.getByRole('heading', {
          name: 'No storage backends match your filters',
          level: 2,
        }),
      ).toBeInTheDocument();
    });
    expect(screen.getByText('Try a different search term.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Clear all filters' })).toBeInTheDocument();
    expect(screen.queryByText('No storage backends are available yet.')).not.toBeInTheDocument();
  });

  it('renders the search input', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByPlaceholderText('Search storage backends')).toBeInTheDocument();
    });
  });

  it('renders the list and card view switcher', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Card view' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'List view' })).toBeInTheDocument();
    });
  });

  it('links the Create backend action to the create route', () => {
    renderPage();

    expect(screen.getByRole('link', { name: 'Create backend' })).toHaveAttribute(
      'href',
      '/admin/infrastructure/storage/backends/create',
    );
  });

  it('navigates to the edit route with the correct ID when Edit is clicked', async () => {
    localStorage.setItem(getViewTypePrefKey('storage-backends'), 'list');
    const { user } = renderPage();

    await waitFor(() => {
      expect(screen.getByText('vast-prod')).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Actions for vast-prod' }));
    await user.click(screen.getByText('Edit'));

    expect(mockNavigate).toHaveBeenCalledWith('/admin/infrastructure/storage/backends/b-1/edit');
  });

  it('opens the delete confirmation dialog when Delete is clicked', async () => {
    localStorage.setItem(getViewTypePrefKey('storage-backends'), 'list');
    const { user } = renderPage();

    await waitFor(() => {
      expect(screen.getByText('vast-prod')).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Actions for vast-prod' }));
    await user.click(screen.getByText('Delete'));

    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(screen.getByText('Delete vast-prod?')).toBeInTheDocument();
  });

  it('removes the row from the table after a successful delete', async () => {
    localStorage.setItem(getViewTypePrefKey('storage-backends'), 'list');
    const { user } = renderPage();

    await waitFor(() => {
      expect(screen.getByText('vast-prod')).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Actions for vast-prod' }));
    await user.click(screen.getByText('Delete'));

    await user.click(screen.getByRole('button', { name: /^Delete$/i }));

    await waitFor(() => {
      expect(screen.queryByText('vast-prod')).not.toBeInTheDocument();
    });
    expect(screen.getByText('ceph-dev')).toBeInTheDocument();
  });
});
