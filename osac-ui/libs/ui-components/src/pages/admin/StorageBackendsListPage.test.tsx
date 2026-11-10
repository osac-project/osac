import { Route, Routes } from 'react-router-dom';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { StorageBackend } from '@osac/types/private';
import { StorageBackendState } from '@osac/types/private';

import { StorageBackendsListPage } from './StorageBackendsListPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

const BACKENDS_LIST_PATH = '/admin/infrastructure/storage/backends';

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

const renderPage = (storageBackends: StorageBackend[] = defaultBackends) =>
  renderWithProviders(
    <Routes>
      <Route path={BACKENDS_LIST_PATH} element={<StorageBackendsListPage />} />
      <Route path={`${BACKENDS_LIST_PATH}/:id/edit`} element={<div>navigated-to-edit</div>} />
    </Routes>,
    { apiFixtures: { storageBackends }, routerEntries: [BACKENDS_LIST_PATH] },
  );

describe('StorageBackendsListPage', () => {
  it('renders column headers', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: 'Name' })).toBeInTheDocument();
    });
    expect(screen.getByRole('columnheader', { name: 'Provider' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Endpoint' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Status' })).toBeInTheDocument();
  });

  it('renders a row per backend with name, provider, and endpoint', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('vast-prod')).toBeInTheDocument();
    });
    expect(screen.getByText('VAST Data')).toBeInTheDocument();
    expect(screen.getByText('vast.example.com')).toBeInTheDocument();
    expect(screen.getByText('ceph-dev')).toBeInTheDocument();
    expect(screen.getByText('ceph')).toBeInTheDocument();
  });

  it('links the backend name to its details page', async () => {
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
        screen.getByText('No storage backends yet. Create one to get started.'),
      ).toBeInTheDocument();
    });
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it('links the Create backend action to the create route', () => {
    renderPage();

    expect(screen.getByRole('link', { name: 'Create backend' })).toHaveAttribute(
      'href',
      '/admin/infrastructure/storage/backends/create',
    );
  });

  it('navigates to the edit route with the correct ID when Edit is clicked', async () => {
    const { user } = renderPage();

    await waitFor(() => {
      expect(screen.getByText('vast-prod')).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Actions for vast-prod' }));
    await user.click(screen.getByText('Edit'));

    expect(await screen.findByText('navigated-to-edit')).toBeInTheDocument();
  });

  it('opens the delete confirmation dialog when Delete is clicked', async () => {
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
