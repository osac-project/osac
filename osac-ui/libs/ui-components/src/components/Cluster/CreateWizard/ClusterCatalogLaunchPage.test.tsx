import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ClusterCatalogItemSchema } from '@osac/types';

import ClusterCatalogLaunchPage from './ClusterCatalogLaunchPage';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const failingGet = () => {
  let calls = 0;
  return () => {
    calls += 1;
    if (calls > 1) {
      throw new Error('Catalog service unavailable');
    }
    return { object: catalogItem };
  };
};

const catalogItem = create(ClusterCatalogItemSchema, {
  id: 'catalog-cluster',
  metadata: { name: 'catalog-cluster', tenant: 'test-tenant' },
  fields: {},
});

const renderLaunchPage = (routerEntry = '/catalog/clusters/catalog-cluster/launch') =>
  renderWithProviders(
    <Routes>
      <Route path="/catalog/clusters" element={<div>Cluster catalog list</div>} />
      <Route path="/catalog/clusters/:id" element={<div>Cluster catalog details</div>} />
      <Route path="/catalog/clusters/:id/launch" element={<ClusterCatalogLaunchPage />} />
    </Routes>,
    {
      routerEntries: [routerEntry],
      apiFixtures: { clusterCatalogItems: [catalogItem] },
      session: { role: 'tenant-user' },
    },
  );

describe('ClusterCatalogLaunchPage', () => {
  it('loads the catalog item and starts the cluster wizard without a catalog step', async () => {
    renderLaunchPage();

    expect(await screen.findByRole('heading', { name: 'Create a cluster' })).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Name' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Catalog item' })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Catalog' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'catalog-cluster' })).toBeInTheDocument();
  });

  it('navigates to the catalog item details when the wizard is cancelled', async () => {
    const { user } = renderLaunchPage();

    await screen.findByRole('textbox', { name: 'Name' });
    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(await screen.findByText('Cluster catalog details')).toBeInTheDocument();
  });

  it('navigates to the catalog list from the breadcrumb', async () => {
    const { user } = renderLaunchPage();

    await screen.findByRole('textbox', { name: 'Name' });
    await user.click(screen.getByRole('link', { name: 'Catalog' }));

    expect(await screen.findByText('Cluster catalog list')).toBeInTheDocument();
  });

  it('keeps the wizard mounted when a background catalog refresh fails', async () => {
    const onGet = failingGet();
    const { queryClient } = renderWithProviders(
      <Routes>
        <Route path="/catalog/clusters/:id/launch" element={<ClusterCatalogLaunchPage />} />
      </Routes>,
      {
        routerEntries: ['/catalog/clusters/catalog-cluster/launch'],
        apiFixtures: { clusterCatalogItems: [catalogItem] },
        transportOverrides: { onClusterCatalogItemGet: onGet },
        session: { role: 'tenant-user' },
      },
    );

    expect(await screen.findByRole('textbox', { name: 'Name' })).toBeInTheDocument();

    // Simulate the production 10s background poll failing after the initial
    // load: the query now has cached data and an error. The wizard must stay
    // mounted instead of being replaced by the error view.
    await queryClient.refetchQueries();

    expect(screen.getByRole('textbox', { name: 'Name' })).toBeInTheDocument();
    expect(
      screen.queryByRole('heading', { name: 'Could not load cluster catalog item' }),
    ).not.toBeInTheDocument();
  });

  it('renders a not-found state when the catalog item does not exist', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/catalog/clusters/:id/launch" element={<ClusterCatalogLaunchPage />} />
      </Routes>,
      {
        routerEntries: ['/catalog/clusters/missing/launch'],
        session: { role: 'tenant-user' },
      },
    );

    expect(
      await screen.findByRole('heading', { name: 'Cluster catalog item not found' }),
    ).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Create a cluster' })).not.toBeInTheDocument();
  });
});
