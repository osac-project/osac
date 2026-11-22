import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { BareMetalInstanceCatalogItemSchema } from '@osac/types';

import BareMetalCatalogLaunchPage from './BareMetalCatalogLaunchPage';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const catalogItem = create(BareMetalInstanceCatalogItemSchema, {
  id: 'catalog-bare-metal',
  metadata: { name: 'catalog-bare-metal', tenant: 'test-tenant' },
  fields: {},
});

const renderLaunchPage = (routerEntry = '/catalog/bare-metal/catalog-bare-metal/launch') =>
  renderWithProviders(
    <Routes>
      <Route path="/catalog/bare-metal" element={<div>Bare metal catalog list</div>} />
      <Route path="/catalog/bare-metal/:id" element={<div>Bare metal catalog details</div>} />
      <Route path="/catalog/bare-metal/:id/launch" element={<BareMetalCatalogLaunchPage />} />
    </Routes>,
    {
      routerEntries: [routerEntry],
      apiFixtures: { bareMetalCatalogItems: [catalogItem] },
      session: { role: 'tenant-user' },
    },
  );

describe('BareMetalCatalogLaunchPage', () => {
  it('loads the catalog item and starts the bare metal wizard without a catalog step', async () => {
    renderLaunchPage();

    expect(
      await screen.findByRole('heading', { name: 'Create a bare metal instance' }),
    ).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Name' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Catalog item' })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Catalog' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'catalog-bare-metal' })).toBeInTheDocument();
  });

  it('navigates to the catalog item details when the wizard is cancelled', async () => {
    const { user } = renderLaunchPage();

    await screen.findByRole('textbox', { name: 'Name' });
    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(await screen.findByText('Bare metal catalog details')).toBeInTheDocument();
  });

  it('navigates to the catalog list from the breadcrumb', async () => {
    const { user } = renderLaunchPage();

    await screen.findByRole('textbox', { name: 'Name' });
    await user.click(screen.getByRole('link', { name: 'Catalog' }));

    expect(await screen.findByText('Bare metal catalog list')).toBeInTheDocument();
  });

  it('renders a not-found state when the catalog item does not exist', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/catalog/bare-metal/:id/launch" element={<BareMetalCatalogLaunchPage />} />
      </Routes>,
      {
        routerEntries: ['/catalog/bare-metal/missing/launch'],
        session: { role: 'tenant-user' },
      },
    );

    expect(
      await screen.findByRole('heading', { name: 'Bare metal catalog item not found' }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('heading', { name: 'Create a bare metal instance' }),
    ).not.toBeInTheDocument();
  });
});
