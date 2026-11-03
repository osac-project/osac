import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ComputeInstanceCatalogItemSchema } from '@osac/types';

import VMCatalogItemDetailPage from './VMCatalogItemDetailPage';
import { renderWithProviders } from '../../../test-utils/TestProviders';

describe('VMCatalogItemDetailPage', () => {
  it('loads a VM catalog item and renders its resource-specific details', async () => {
    const item = create(ComputeInstanceCatalogItemSchema, {
      id: 'catalog-vm',
      metadata: { name: 'catalog-vm', tenant: 'test-tenant' },
      fields: {},
    });

    renderWithProviders(
      <Routes>
        <Route path="/catalog/vm/:id" element={<VMCatalogItemDetailPage />} />
      </Routes>,
      {
        routerEntries: ['/catalog/vm/catalog-vm'],
        apiFixtures: { catalogItems: [item] },
      },
    );

    expect(
      await screen.findByRole('heading', { name: 'catalog-vm', level: 1 }),
    ).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Overview' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Configuration' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Access & Initial run' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Storage' })).toBeInTheDocument();
  });
});
