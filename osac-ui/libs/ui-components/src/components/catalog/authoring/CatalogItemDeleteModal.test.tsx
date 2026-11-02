import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type {
  BareMetalInstanceCatalogItem,
  ClusterCatalogItem,
  ComputeInstanceCatalogItem,
} from '@osac/types';

import CatalogItemDeleteModal from './CatalogItemDeleteModal';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const vmItem: ComputeInstanceCatalogItem = {
  $typeName: 'osac.public.v1.ComputeInstanceCatalogItem',
  id: 'catalog-1',
  metadata: { name: 'rhel-9-catalog' },
} as unknown as ComputeInstanceCatalogItem;

const clusterItem: ClusterCatalogItem = {
  $typeName: 'osac.public.v1.ClusterCatalogItem',
  id: 'catalog-2',
  metadata: { name: 'openshift-catalog' },
} as unknown as ClusterCatalogItem;

const bmItem: BareMetalInstanceCatalogItem = {
  $typeName: 'osac.public.v1.BareMetalInstanceCatalogItem',
  id: 'catalog-3',
  metadata: { name: 'bm-catalog' },
} as unknown as BareMetalInstanceCatalogItem;

describe('CatalogItemDeleteModal', () => {
  it('warns that existing provisioned resources are unaffected', () => {
    renderWithProviders(
      <CatalogItemDeleteModal item={vmItem} onClose={vi.fn()} onSuccess={vi.fn()} />,
    );

    expect(
      screen.getByText(/does not affect resources that tenants have already provisioned/i),
    ).toBeInTheDocument();
  });

  it('deletes a compute instance catalog item and calls onSuccess', async () => {
    const onSuccess = vi.fn();
    const { user } = renderWithProviders(
      <CatalogItemDeleteModal item={vmItem} onClose={vi.fn()} onSuccess={onSuccess} />,
    );

    await user.click(screen.getByRole('button', { name: /^Delete$/i }));

    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
  });

  it('deletes a cluster catalog item and calls onSuccess', async () => {
    const onSuccess = vi.fn();
    const { user } = renderWithProviders(
      <CatalogItemDeleteModal item={clusterItem} onClose={vi.fn()} onSuccess={onSuccess} />,
    );

    await user.click(screen.getByRole('button', { name: /^Delete$/i }));

    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
  });

  it('deletes a bare metal catalog item and calls onSuccess', async () => {
    const onSuccess = vi.fn();
    const { user } = renderWithProviders(
      <CatalogItemDeleteModal item={bmItem} onClose={vi.fn()} onSuccess={onSuccess} />,
    );

    await user.click(screen.getByRole('button', { name: /^Delete$/i }));

    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
  });

  it('shows an error and does not call onSuccess when delete fails', async () => {
    const onSuccess = vi.fn();
    const { user } = renderWithProviders(
      <CatalogItemDeleteModal item={vmItem} onClose={vi.fn()} onSuccess={onSuccess} />,
      {
        transportOverrides: {
          onComputeInstanceCatalogItemDelete: () => {
            throw new ConnectError('permission denied', Code.PermissionDenied);
          },
        },
      },
    );

    await user.click(screen.getByRole('button', { name: /^Delete$/i }));

    await waitFor(() =>
      expect(screen.getByText('Failed to delete catalog item')).toBeInTheDocument(),
    );
    expect(onSuccess).not.toHaveBeenCalled();
  });

  it('closes without deleting on Cancel', async () => {
    const onClose = vi.fn();
    const { user } = renderWithProviders(
      <CatalogItemDeleteModal item={vmItem} onClose={onClose} onSuccess={vi.fn()} />,
    );

    await user.click(screen.getByRole('button', { name: /Cancel/i }));

    expect(onClose).toHaveBeenCalled();
  });
});
