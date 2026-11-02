import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  type ClusterCatalogItem,
  ClusterTemplateReferenceSchema,
  type ComputeInstanceCatalogItem,
  ComputeInstanceTemplateReferenceSchema,
} from '@osac/types';
import { UserRole } from '@osac/ui-components/shellTypes';

import CatalogItemActionsMenu from './CatalogItemActionsMenu';
import type { MockTransportOverrides } from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

const VM_NAME = 'catalog-rhel-9';
const UNPUBLISHED_VM_NAME = 'catalog-rhel-10';
const CLUSTER_NAME = 'catalog-openshift-4';

const vmCatalogItem: ComputeInstanceCatalogItem = {
  $typeName: 'osac.public.v1.ComputeInstanceCatalogItem',
  id: 'catalog-rhel-9',
  metadata: {
    $typeName: 'osac.public.v1.Metadata',
    displayName: '',
    description: '',
    name: VM_NAME,
    annotations: {},
    creator: 'foo',
    labels: {},
    project: 'foo',
    tenant: 'foo',
    version: 1,
  },
  title: 'RHEL 9 catalog',
  description: 'RHEL 9 base image',
  template: create(ComputeInstanceTemplateReferenceSchema, { id: 'tpl-rhel-9' }),
  published: true,
  templateParameters: {},
};

const unpublishedVmCatalogItem: ComputeInstanceCatalogItem = {
  ...vmCatalogItem,
  id: 'catalog-rhel-10',
  metadata: {
    $typeName: 'osac.public.v1.Metadata',
    displayName: '',
    description: '',
    name: UNPUBLISHED_VM_NAME,
    annotations: {},
    creator: 'foo',
    labels: {},
    project: 'foo',
    tenant: 'foo',
    version: 1,
  },
  published: false,
};

const clusterCatalogItem: ClusterCatalogItem = {
  $typeName: 'osac.public.v1.ClusterCatalogItem',
  id: 'catalog-openshift-4',
  metadata: {
    $typeName: 'osac.public.v1.Metadata',
    displayName: '',
    description: '',
    name: CLUSTER_NAME,
    creator: 'admin',
    annotations: {},
    labels: {},
    project: 'foo',
    tenant: 'foo',
    version: 1,
  },
  title: 'OpenShift 4 cluster',
  description: 'Standard OpenShift cluster offering',
  template: create(ClusterTemplateReferenceSchema, { id: 'tpl-openshift-4' }),
  published: true,
  templateParameters: {},
};

const renderMenu = (
  item: ComputeInstanceCatalogItem | ClusterCatalogItem,
  role: UserRole,
  tenantId = 'foo',
  transportOverrides?: MockTransportOverrides,
) =>
  renderWithProviders(
    <Routes>
      <Route
        path="/"
        element={<CatalogItemActionsMenu item={item} role={role} tenantId={tenantId} />}
      />
      <Route path="/catalog/:kind/:id" element={<div>Catalog item details page</div>} />
      <Route path="/catalog/:kind/:id/edit" element={<div>Edit catalog item page</div>} />
      <Route path="/clusters/create/:catalogItemId" element={<div>Create cluster page</div>} />
      <Route path="/vms/create/:catalogItemId" element={<div>Create virtual machine page</div>} />
    </Routes>,
    { routerEntries: ['/'], transportOverrides },
  );

const openMenu = async (user: { click: (el: Element) => Promise<void> }, name: string) => {
  await user.click(screen.getByRole('button', { name: `Actions for ${name}` }));
};

describe('CatalogItemActionsMenu', () => {
  it('opens a kebab with view details and create actions for tenant admins', async () => {
    const { user } = renderMenu(vmCatalogItem, 'tenant-admin');

    await openMenu(user, VM_NAME);

    expect(screen.getByRole('menuitem', { name: 'View details' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Launch instance' })).toBeInTheDocument();
  });

  it('navigates to the catalog item details from View details', async () => {
    const { user } = renderMenu(vmCatalogItem, 'tenant-admin');

    await openMenu(user, VM_NAME);
    await user.click(screen.getByRole('menuitem', { name: 'View details' }));

    await waitFor(() => {
      expect(screen.getByText('Catalog item details page')).toBeInTheDocument();
    });
  });

  it('navigates to the create path from Launch instance for tenant admins', async () => {
    const { user } = renderMenu(vmCatalogItem, 'tenant-admin');

    await openMenu(user, VM_NAME);
    await user.click(screen.getByRole('menuitem', { name: 'Launch instance' }));

    await waitFor(() => {
      expect(screen.getByText('Create virtual machine page')).toBeInTheDocument();
    });
  });

  it('navigates to the create path from Launch instance for tenant users', async () => {
    const { user } = renderMenu(vmCatalogItem, 'tenant-user');

    await openMenu(user, VM_NAME);
    await user.click(screen.getByRole('menuitem', { name: 'Launch instance' }));

    await waitFor(() => {
      expect(screen.getByText('Create virtual machine page')).toBeInTheDocument();
    });
  });

  it('navigates to the cluster create path for a cluster catalog item', async () => {
    const { user } = renderMenu(clusterCatalogItem, 'tenant-admin');

    await openMenu(user, CLUSTER_NAME);
    await user.click(screen.getByRole('menuitem', { name: 'Launch instance' }));

    await waitFor(() => {
      expect(screen.getByText('Create cluster page')).toBeInTheDocument();
    });
  });

  it('hides launch instance and shows lifecycle actions for provider admins', async () => {
    const { user } = renderMenu(clusterCatalogItem, 'admin');

    await openMenu(user, CLUSTER_NAME);

    expect(screen.getByRole('menuitem', { name: 'View details' })).toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Launch instance' })).not.toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Edit' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Unpublish' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Delete' })).toBeInTheDocument();
  });

  it('shows Publish for an unpublished item', async () => {
    const { user } = renderMenu(unpublishedVmCatalogItem, 'admin');

    await openMenu(user, UNPUBLISHED_VM_NAME);

    expect(screen.getByRole('menuitem', { name: 'Publish' })).toBeInTheDocument();
  });

  it('shows lifecycle actions for a tenant-admin managing an item in their own tenant', async () => {
    const { user } = renderMenu(vmCatalogItem, 'tenant-admin', 'foo');

    await openMenu(user, VM_NAME);

    expect(screen.getByRole('menuitem', { name: 'Edit' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Unpublish' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Delete' })).toBeInTheDocument();
  });

  it('hides lifecycle actions for a tenant-admin outside the item tenant', async () => {
    const { user } = renderMenu(vmCatalogItem, 'tenant-admin', 'other-tenant');

    await openMenu(user, VM_NAME);

    expect(screen.queryByRole('menuitem', { name: 'Edit' })).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Unpublish' })).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Delete' })).not.toBeInTheDocument();
  });

  it('hides lifecycle actions for a tenant-user', async () => {
    const { user } = renderMenu(vmCatalogItem, 'tenant-user', 'foo');

    await openMenu(user, VM_NAME);

    expect(screen.queryByRole('menuitem', { name: 'Edit' })).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Unpublish' })).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Delete' })).not.toBeInTheDocument();
  });

  it('navigates to the edit path from Edit', async () => {
    const { user } = renderMenu(vmCatalogItem, 'admin');

    await openMenu(user, VM_NAME);
    await user.click(screen.getByRole('menuitem', { name: 'Edit' }));

    await waitFor(() => {
      expect(screen.getByText('Edit catalog item page')).toBeInTheDocument();
    });
  });

  it('sends only id and published when unpublishing a published item', async () => {
    let sentObject: unknown;
    const { user } = renderMenu(vmCatalogItem, 'admin', 'foo', {
      onComputeInstanceCatalogItemUpdate: (req) => {
        sentObject = req.object;
        return { object: { ...vmCatalogItem, published: false } };
      },
    });

    await openMenu(user, VM_NAME);
    await user.click(screen.getByRole('menuitem', { name: 'Unpublish' }));

    await waitFor(() => {
      expect(sentObject).toMatchObject({ id: vmCatalogItem.id, published: false });
    });
  });

  it('shows an error toast and keeps the item unchanged when publish fails', async () => {
    const { user } = renderMenu(unpublishedVmCatalogItem, 'admin', 'foo', {
      onComputeInstanceCatalogItemUpdate: () => {
        throw new ConnectError('permission denied', Code.PermissionDenied);
      },
    });

    await openMenu(user, UNPUBLISHED_VM_NAME);
    await user.click(screen.getByRole('menuitem', { name: 'Publish' }));

    await waitFor(() => {
      expect(screen.getByText('Failed to publish catalog item')).toBeInTheDocument();
    });
  });

  it('deletes the item through the Delete confirmation modal', async () => {
    const { user } = renderMenu(vmCatalogItem, 'admin');

    await openMenu(user, VM_NAME);
    await user.click(screen.getByRole('menuitem', { name: 'Delete' }));

    expect(screen.getByRole('dialog')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: /^Delete$/i }));

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
  });

  it('shows an error and keeps the modal open when delete fails', async () => {
    const { user } = renderMenu(vmCatalogItem, 'admin', 'foo', {
      onComputeInstanceCatalogItemDelete: () => {
        throw new ConnectError('permission denied', Code.PermissionDenied);
      },
    });

    await openMenu(user, VM_NAME);
    await user.click(screen.getByRole('menuitem', { name: 'Delete' }));
    await user.click(screen.getByRole('button', { name: /^Delete$/i }));

    await waitFor(() => {
      expect(screen.getByText('Failed to delete catalog item')).toBeInTheDocument();
    });
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });
});
