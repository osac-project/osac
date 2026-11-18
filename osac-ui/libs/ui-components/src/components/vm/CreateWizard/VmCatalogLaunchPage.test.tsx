import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ComputeInstanceCatalogItemSchema } from '@osac/types';

import VmCatalogLaunchPage from './VmCatalogLaunchPage';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const catalogItem = create(ComputeInstanceCatalogItemSchema, {
  id: 'catalog-vm',
  metadata: { name: 'catalog-vm', tenant: 'test-tenant' },
  fields: {},
});

const renderLaunchPage = (routerEntry = '/catalog/vms/catalog-vm/launch') =>
  renderWithProviders(
    <Routes>
      <Route path="/catalog/vms" element={<div>VM catalog list</div>} />
      <Route path="/catalog/vms/:id" element={<div>VM catalog details</div>} />
      <Route path="/catalog/vms/:id/launch" element={<VmCatalogLaunchPage />} />
    </Routes>,
    {
      routerEntries: [routerEntry],
      apiFixtures: { catalogItems: [catalogItem] },
      session: { role: 'tenant-user' },
    },
  );

describe('VmCatalogLaunchPage', () => {
  it('loads the catalog item and starts the VM wizard without a catalog step', async () => {
    renderLaunchPage();

    expect(
      await screen.findByRole('heading', { name: 'Create a virtual machine' }),
    ).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Name' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Catalog item' })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Catalog' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'catalog-vm' })).toBeInTheDocument();
  });

  it('navigates to the catalog item details when the wizard is cancelled', async () => {
    const { user } = renderLaunchPage();

    await screen.findByRole('textbox', { name: 'Name' });
    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(await screen.findByText('VM catalog details')).toBeInTheDocument();
  });

  it('navigates to the catalog list from the breadcrumb', async () => {
    const { user } = renderLaunchPage();

    await screen.findByRole('textbox', { name: 'Name' });
    await user.click(screen.getByRole('link', { name: 'Catalog' }));

    expect(await screen.findByText('VM catalog list')).toBeInTheDocument();
  });

  it('renders a not-found state when the catalog item does not exist', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/catalog/vms/:id/launch" element={<VmCatalogLaunchPage />} />
      </Routes>,
      {
        routerEntries: ['/catalog/vms/missing/launch'],
        session: { role: 'tenant-user' },
      },
    );

    expect(
      await screen.findByRole('heading', { name: 'VM catalog item not found' }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('heading', { name: 'Create a virtual machine' }),
    ).not.toBeInTheDocument();
  });
});
