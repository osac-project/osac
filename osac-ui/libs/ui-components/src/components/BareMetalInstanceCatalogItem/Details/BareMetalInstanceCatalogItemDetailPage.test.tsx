import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { BareMetalInstanceCatalogItemSchema } from '@osac/types';

import BareMetalInstanceCatalogItemDetailPage from './BareMetalInstanceCatalogItemDetailPage';
import { renderWithProviders } from '../../../test-utils/TestProviders';

describe('BareMetalInstanceCatalogItemDetailPage', () => {
  it('loads a bare metal catalog item and renders its resource-specific details', async () => {
    const item = create(BareMetalInstanceCatalogItemSchema, {
      id: 'catalog-bare-metal',
      metadata: { name: 'catalog-bare-metal', tenant: 'test-tenant' },
      fields: {},
    });

    renderWithProviders(
      <Routes>
        <Route path="/catalog/bm/:id" element={<BareMetalInstanceCatalogItemDetailPage />} />
      </Routes>,
      {
        routerEntries: ['/catalog/bm/catalog-bare-metal'],
        apiFixtures: { bareMetalCatalogItems: [item] },
      },
    );

    expect(
      await screen.findByRole('heading', { name: 'catalog-bare-metal', level: 1 }),
    ).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Overview' })).toBeInTheDocument();
    expect(screen.getByText('Configuration')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Storage' })).not.toBeInTheDocument();
  });
});
