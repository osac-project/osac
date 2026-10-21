import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { ComputeInstanceCatalogItemSchema, ComputeInstanceTemplateSchema } from '@osac/types';

import { CatalogItemEditPage } from './CatalogItemEditPage';
import { renderWithProviders } from '../../../test-utils/TestProviders';

vi.mock('../../../hooks/use-session', () => ({
  useSession: () => ({ role: 'tenant-admin', tenantId: 'tenant-1' }),
}));

describe('CatalogItemEditPage', () => {
  it('loads the item and renders its service wizard in edit mode', async () => {
    const item = create(ComputeInstanceCatalogItemSchema, {
      id: 'catalog-vm',
      metadata: { name: 'catalog-vm', tenant: 'tenant-1' },
      template: { id: 'template-1' },
      fields: {},
    });

    renderWithProviders(
      <Routes>
        <Route path="/catalog/:kind/:id/edit" element={<CatalogItemEditPage />} />
      </Routes>,
      {
        routerEntries: ['/catalog/vm/catalog-vm/edit'],
        apiFixtures: {
          catalogItems: [item],
          computeInstanceTemplates: [
            create(ComputeInstanceTemplateSchema, {
              id: 'template-1',
              metadata: { name: 'Template 1' },
            }),
          ],
          diskImages: [],
          instanceTypes: [],
        },
      },
    );

    expect(await screen.findByRole('heading', { name: 'Edit catalog item' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'General' })).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Name' })).toHaveValue('catalog-vm');
  });
});
