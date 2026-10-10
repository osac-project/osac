import { Route, Routes } from 'react-router-dom';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { StorageManagementPage } from './StorageManagementPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

const renderPage = (activeTab: 'backends' | 'tiers') =>
  renderWithProviders(
    <Routes>
      <Route
        path="/admin/infrastructure/storage/backends"
        element={<StorageManagementPage activeTab="backends" />}
      />
      <Route
        path="/admin/infrastructure/storage/tiers"
        element={<StorageManagementPage activeTab="tiers" />}
      />
    </Routes>,
    { routerEntries: [`/admin/infrastructure/storage/${activeTab}`] },
  );

describe('StorageManagementPage', () => {
  it('renders Backends and Tiers tabs', () => {
    renderPage('backends');

    expect(screen.getByRole('tab', { name: 'Backends' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Tiers' })).toBeInTheDocument();
  });

  it('shows the storage backends list when activeTab is backends', async () => {
    renderPage('backends');

    await waitFor(() => {
      expect(
        screen.getByText('No storage backends yet. Create one to get started.'),
      ).toBeInTheDocument();
    });
  });

  it('renders the Storage Tiers list page when activeTab is tiers', async () => {
    renderPage('tiers');

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'Create tier' })).toBeInTheDocument();
    });
  });

  it('navigates to /admin/infrastructure/storage/tiers when the Tiers tab is clicked', async () => {
    const { user } = renderPage('backends');

    await user.click(screen.getByRole('tab', { name: 'Tiers' }));

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'Create tier' })).toBeInTheDocument();
    });
  });

  it('does not mount the Tiers list page (and its data fetches) while the Backends tab is active', async () => {
    const onStorageTierList = vi.fn(() => ({ items: [], size: 0, total: 0 }));

    renderWithProviders(
      <Routes>
        <Route
          path="/admin/infrastructure/storage/backends"
          element={<StorageManagementPage activeTab="backends" />}
        />
      </Routes>,
      {
        routerEntries: ['/admin/infrastructure/storage/backends'],
        transportOverrides: { onStorageTierList },
      },
    );

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'Create backend' })).toBeInTheDocument();
    });
    expect(onStorageTierList).not.toHaveBeenCalled();
  });

  it('unmounts the Tiers list page (and stops its data fetches) after switching away to the Backends tab', async () => {
    const onStorageTierList = vi.fn(() => ({ items: [], size: 0, total: 0 }));

    const { user } = renderWithProviders(
      <Routes>
        <Route
          path="/admin/infrastructure/storage/backends"
          element={<StorageManagementPage activeTab="backends" />}
        />
        <Route
          path="/admin/infrastructure/storage/tiers"
          element={<StorageManagementPage activeTab="tiers" />}
        />
      </Routes>,
      {
        routerEntries: ['/admin/infrastructure/storage/tiers'],
        transportOverrides: { onStorageTierList },
      },
    );

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'Create tier' })).toBeInTheDocument();
    });
    expect(onStorageTierList).toHaveBeenCalledTimes(1);

    await user.click(screen.getByRole('tab', { name: 'Backends' }));

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'Create backend' })).toBeInTheDocument();
    });
    expect(screen.queryByRole('link', { name: 'Create tier' })).not.toBeInTheDocument();
    expect(onStorageTierList).toHaveBeenCalledTimes(1);
  });
});
