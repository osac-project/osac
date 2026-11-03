import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ClusterCatalogItemSchema } from '@osac/types';

import ClusterCatalogItemDetailPage from './ClusterCatalogItemDetailPage';
import { renderWithProviders } from '../../../test-utils/TestProviders';

describe('ClusterCatalogItemDetailPage', () => {
  it('loads a cluster catalog item and renders its resource-specific details', async () => {
    const item = create(ClusterCatalogItemSchema, {
      id: 'catalog-cluster',
      metadata: { name: 'catalog-cluster', tenant: 'test-tenant' },
      fields: {},
    });

    renderWithProviders(
      <Routes>
        <Route path="/catalog/cluster/:id" element={<ClusterCatalogItemDetailPage />} />
      </Routes>,
      {
        routerEntries: ['/catalog/cluster/catalog-cluster'],
        apiFixtures: { clusterCatalogItems: [item] },
      },
    );

    expect(
      await screen.findByRole('heading', { name: 'catalog-cluster', level: 1 }),
    ).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Overview' })).toBeInTheDocument();
    expect(screen.getByText('Configuration')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Storage' })).not.toBeInTheDocument();
  });
});
