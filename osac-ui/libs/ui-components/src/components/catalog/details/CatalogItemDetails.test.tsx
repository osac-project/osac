import { Route, Routes } from 'react-router-dom';
import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { ComputeInstanceCatalogItem } from '@osac/types';

import CatalogItemDetails from './CatalogItemDetails';
import VmCatalogItemDetailContent from './ComputeCatalogItemDetailContent';
import { CatalogItemIcon } from '../../../icons';
import type { UserRole } from '../../../shellTypes';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const session = vi.hoisted(() => ({
  role: 'admin' as UserRole,
  tenantId: 'tenant-1',
}));

vi.mock('../../../hooks/use-session', () => ({
  useSession: () => session,
}));

const makeItem = (published: boolean): ComputeInstanceCatalogItem =>
  ({
    $typeName: 'osac.public.v1.ComputeInstanceCatalogItem',
    id: 'catalog-vm',
    metadata: {
      name: 'catalog-vm',
      description: 'A virtual machine catalog item',
      tenant: 'tenant-1',
      creationTimestamp: { seconds: 1_735_689_600n, nanos: 0 },
    },
    description: 'A virtual machine catalog item',
    published,
    fields: {},
  }) as ComputeInstanceCatalogItem;

const renderDetails = (
  item: ComputeInstanceCatalogItem,
  transportOverrides: Record<string, unknown> = {},
) =>
  renderWithProviders(
    <Routes>
      <Route
        path="/catalog/vm/catalog-vm"
        element={
          <CatalogItemDetails
            item={item}
            showStatus={false}
            titlePrefix={<CatalogItemIcon kind="vm" isActive />}
          >
            <VmCatalogItemDetailContent item={item} />
          </CatalogItemDetails>
        }
      />
      <Route path="/catalog/vm/catalog-vm/edit" element={<div>Edit catalog item page</div>} />
      <Route path="/catalog" element={<div>Catalog page</div>} />
    </Routes>,
    { routerEntries: ['/catalog/vm/catalog-vm'], transportOverrides: transportOverrides as never },
  );

describe('CatalogItemDetails', () => {
  beforeEach(() => {
    session.role = 'admin';
    session.tenantId = 'tenant-1';
  });

  it('shows lifecycle state, overview metadata, and admin header actions', () => {
    renderDetails(makeItem(false));

    expect(screen.getAllByText('Unpublished').length).toBeGreaterThan(0);
    expect(screen.getByText('Created')).toBeInTheDocument();
    expect(screen.getByText('Visibility')).toBeInTheDocument();
    expect(screen.getByText('tenant-1')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Edit' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Publish' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Launch instance' })).not.toBeInTheDocument();
  });

  it('shows launch and management actions for a tenant admin in the item tenant', () => {
    session.role = 'tenant-admin';
    renderDetails(makeItem(true));

    expect(screen.getAllByText('Live').length).toBeGreaterThan(0);
    expect(screen.getByRole('button', { name: 'Launch instance' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Edit' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Unpublish' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument();
  });

  it('keeps launch for tenant users but hides management actions', () => {
    session.role = 'tenant-user';
    renderDetails(makeItem(true));

    expect(screen.getByRole('button', { name: 'Launch instance' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Unpublish' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument();
  });

  it('navigates to edit from the header action', async () => {
    const { user } = renderDetails(makeItem(true));

    await user.click(screen.getByRole('button', { name: 'Edit' }));

    expect(await screen.findByText('Edit catalog item page')).toBeInTheDocument();
  });

  it('publishes through the public catalog update service', async () => {
    let sentObject: unknown;
    const { user } = renderDetails(makeItem(false), {
      onComputeInstanceCatalogItemUpdate: (request: { object?: unknown }) => {
        sentObject = request.object;
        return { object: makeItem(true) };
      },
    });

    await user.click(screen.getByRole('button', { name: 'Publish' }));

    await waitFor(() => {
      expect(sentObject).toMatchObject({ id: 'catalog-vm', published: true });
    });
  });
});
