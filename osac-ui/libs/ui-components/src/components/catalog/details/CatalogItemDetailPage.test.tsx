import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { ComputeInstanceCatalogItemSchema } from '@osac/types';

import { CatalogItemDetailPage } from './CatalogItemDetailPage';
import { renderWithProviders } from '../../../test-utils/TestProviders';

vi.mock('../../../hooks/use-session', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../hooks/use-session')>();
  return {
    ...actual,
    useSession: () => ({ role: 'admin', tenantId: 'tenant-1' }),
  };
});

describe('CatalogItemDetailPage', () => {
  it('loads a VM catalog item and renders its four detail sections', async () => {
    const item = create(ComputeInstanceCatalogItemSchema, {
      id: 'catalog-vm',
      metadata: { name: 'catalog-vm', tenant: 'tenant-1' },
      fields: {},
    });

    renderWithProviders(
      <Routes>
        <Route path="/catalog/:kind/:id" element={<CatalogItemDetailPage />} />
      </Routes>,
      {
        routerEntries: ['/catalog/vm/catalog-vm'],
        apiFixtures: { catalogItems: [item], diskImages: [], instanceTypes: [] },
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
