import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
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
import { renderWithProviders } from '../../test-utils/TestProviders';

const vmCatalogItem: ComputeInstanceCatalogItem = {
  $typeName: 'osac.public.v1.ComputeInstanceCatalogItem',
  id: 'catalog-rhel-9',
  metadata: {
    $typeName: 'osac.public.v1.Metadata',
    displayName: '',
    description: '',
    name: 'catalog-rhel-9',
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

const clusterCatalogItem: ClusterCatalogItem = {
  $typeName: 'osac.public.v1.ClusterCatalogItem',
  id: 'catalog-openshift-4',
  metadata: {
    $typeName: 'osac.public.v1.Metadata',
    displayName: '',
    description: '',
    name: 'catalog-openshift-4',
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

const renderMenu = (item: ComputeInstanceCatalogItem | ClusterCatalogItem, role: UserRole) =>
  renderWithProviders(
    <Routes>
      <Route path="/" element={<CatalogItemActionsMenu item={item} role={role} />} />
      <Route path="/catalog/:kind/:id" element={<div>Catalog item details page</div>} />
      <Route path="/clusters/create/:catalogItemId" element={<div>Create cluster page</div>} />
      <Route path="/vms/create/:catalogItemId" element={<div>Create virtual machine page</div>} />
    </Routes>,
    { routerEntries: ['/'] },
  );

describe('CatalogItemActionsMenu', () => {
  it('opens a kebab with view details and create actions for tenant admins', async () => {
    const { user } = renderMenu(vmCatalogItem, 'tenant-admin');

    await user.click(
      screen.getByRole('button', { name: `Actions for ${vmCatalogItem.metadata?.name}` }),
    );

    expect(screen.getByRole('menuitem', { name: 'View details' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Launch instance' })).toBeInTheDocument();
  });

  it('navigates to the catalog item details from View details', async () => {
    const { user } = renderMenu(vmCatalogItem, 'tenant-admin');

    await user.click(
      screen.getByRole('button', { name: `Actions for ${vmCatalogItem.metadata?.name}` }),
    );
    await user.click(screen.getByRole('menuitem', { name: 'View details' }));

    await waitFor(() => {
      expect(screen.getByText('Catalog item details page')).toBeInTheDocument();
    });
  });

  it('navigates to the create path from Launch instance for tenant admins', async () => {
    const { user } = renderMenu(vmCatalogItem, 'tenant-admin');

    await user.click(
      screen.getByRole('button', { name: `Actions for ${vmCatalogItem.metadata?.name}` }),
    );
    await user.click(screen.getByRole('menuitem', { name: 'Launch instance' }));

    await waitFor(() => {
      expect(screen.getByText('Create virtual machine page')).toBeInTheDocument();
    });
  });

  it('navigates to the create path from Launch instance for tenant users', async () => {
    const { user } = renderMenu(vmCatalogItem, 'tenant-user');

    await user.click(
      screen.getByRole('button', { name: `Actions for ${vmCatalogItem.metadata?.name}` }),
    );
    await user.click(screen.getByRole('menuitem', { name: 'Launch instance' }));

    await waitFor(() => {
      expect(screen.getByText('Create virtual machine page')).toBeInTheDocument();
    });
  });

  it('navigates to the cluster create path for a cluster catalog item', async () => {
    const { user } = renderMenu(clusterCatalogItem, 'tenant-admin');

    await user.click(
      screen.getByRole('button', { name: `Actions for ${clusterCatalogItem.metadata?.name}` }),
    );
    await user.click(screen.getByRole('menuitem', { name: 'Launch instance' }));

    await waitFor(() => {
      expect(screen.getByText('Create cluster page')).toBeInTheDocument();
    });
  });

  it('hides to the create launch instance action for provider admins', async () => {
    const { user } = renderMenu(clusterCatalogItem, 'admin');

    await user.click(
      screen.getByRole('button', { name: `Actions for ${clusterCatalogItem.metadata?.name}` }),
    );

    expect(screen.getByRole('menuitem', { name: 'View details' })).toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Launch instance' })).not.toBeInTheDocument();
  });
});
