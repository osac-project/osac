import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { ComputeInstanceCatalogItemSchema } from '@osac/types';

import { VmCatalogItemDetailPage } from './VmCatalogItemDetailPage';
import { renderWithProviders } from '../../../test-utils/TestProviders';

vi.mock('../../../hooks/use-session', () => ({
  useSession: () => ({ role: 'admin', tenantId: 'tenant-1' }),
}));

describe('VmCatalogItemDetailPage', () => {
  it('loads a VM catalog item and renders its four detail cards', async () => {
    const item = create(ComputeInstanceCatalogItemSchema, {
      id: 'catalog-vm',
      metadata: { name: 'catalog-vm', tenant: 'tenant-1' },
      fields: {},
    });

    renderWithProviders(
      <Routes>
        <Route path="/catalog/vm/:id" element={<VmCatalogItemDetailPage />} />
      </Routes>,
      {
        routerEntries: ['/catalog/vm/catalog-vm'],
        apiFixtures: { catalogItems: [item], diskImages: [], instanceTypes: [] },
      },
    );

    expect(
      await screen.findByRole('heading', { name: 'catalog-vm', level: 1 }),
    ).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Overview' })).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Configuration' })).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Access & Initial run' })).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Storage' })).toBeInTheDocument();
  });
});
