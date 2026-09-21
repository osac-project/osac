import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  BareMetalInstanceCatalogItem,
  BareMetalInstanceCatalogItems,
  ClusterCatalogItem,
  ClusterCatalogItems,
  ClusterTemplateReferenceSchema,
  ComputeInstanceCatalogItem,
  ComputeInstanceCatalogItems,
  ComputeInstanceTemplateReferenceSchema,
} from '@osac/types';

import CatalogPage from './CatalogPage';
import { CatalogItemDetailPage } from '../../components/catalog/details/CatalogItemDetailPage.tsx';
import { wrapWithAuthInterceptor } from '../../test-utils/createMockConnectTransport';
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

const delay = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

type CatalogTransportOptions = {
  vmItems?: ComputeInstanceCatalogItem[];
  clusterItems?: ClusterCatalogItem[];
  bmItems?: BareMetalInstanceCatalogItem[];
  vmError?: Error;
  clusterError?: Error;
  bmError?: Error;
  vmDelayMs?: number;
  clusterDelayMs?: number;
  bmDelayMs?: number;
};

const toConnectError = (error: Error) => {
  if (error.name === 'UnauthorizedError') {
    return new ConnectError('Unauthenticated', Code.Unauthenticated);
  }
  return new ConnectError(error.message, Code.Internal);
};

const createCatalogPageTransport = ({
  vmItems = [vmCatalogItem],
  clusterItems = [clusterCatalogItem],
  bmItems = [],
  vmError,
  clusterError,
  bmError,
  vmDelayMs = 0,
  clusterDelayMs = 0,
  bmDelayMs = 0,
}: CatalogTransportOptions = {}) =>
  wrapWithAuthInterceptor(
    createRouterTransport((router) => {
      router.service(ComputeInstanceCatalogItems, {
        list: async () => {
          if (vmDelayMs) {
            await delay(vmDelayMs);
          }
          if (vmError) {
            throw toConnectError(vmError);
          }
          return { items: vmItems };
        },
        get: (req) => ({
          object: vmItems.find((i) => i.id === req.id),
        }),
      });

      router.service(ClusterCatalogItems, {
        list: async () => {
          if (clusterDelayMs) {
            await delay(clusterDelayMs);
          }
          if (clusterError) {
            throw toConnectError(clusterError);
          }
          return { items: clusterItems };
        },
        get: (req) => ({
          object: clusterItems.find((i) => i.id === req.id),
        }),
      });

      router.service(BareMetalInstanceCatalogItems, {
        list: async () => {
          if (bmDelayMs) {
            await delay(bmDelayMs);
          }
          if (bmError) {
            throw toConnectError(bmError);
          }
          return { items: bmItems };
        },
        get: (req) => ({
          object: bmItems.find((i) => i.id === req.id),
        }),
      });
    }),
  );

const unauthorizedTransport = createCatalogPageTransport({
  vmError: Object.assign(new Error(), { name: 'UnauthorizedError' }),
  clusterError: Object.assign(new Error(), { name: 'UnauthorizedError' }),
  bmError: Object.assign(new Error(), { name: 'UnauthorizedError' }),
});

const renderCatalogPage = (transport = unauthorizedTransport) =>
  renderWithProviders(<CatalogPage />, { transport });

const renderCatalogPageWithDetailRoutes = (transport = createCatalogPageTransport()) =>
  renderWithProviders(
    <Routes>
      <Route path="/catalog" element={<CatalogPage />} />
      <Route path="/catalog/:kind/:id" element={<CatalogItemDetailPage />} />
      <Route path="/clusters/create/:catalogItemId" element={<div>Create cluster page</div>} />
      <Route path="/vms/create/:catalogItemId" element={<div>Create virtual machine page</div>} />
    </Routes>,
    { transport, routerEntries: ['/catalog'] },
  );

describe('CatalogPage', () => {
  it('keeps type filter toggles in the DOM when catalog queries return 401', async () => {
    renderCatalogPage();

    await waitFor(() => {
      expect(screen.getByText('Unauthorized')).toBeInTheDocument();
    });

    expect(screen.getByText('Global marketplace').closest('.pf-v6-c-label')).not.toBeNull();
    expect(
      screen.getByRole('group', { name: 'Filter catalog by resource type' }),
    ).toBeInTheDocument();
    expect(document.getElementById('catalog-type-filter-vm')).toBeInTheDocument();
    expect(document.getElementById('catalog-type-filter-cluster')).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Filter catalog by keyword' })).toBeInTheDocument();
  });

  it('lets users toggle type filters after a 401', async () => {
    const { user } = renderCatalogPage();

    await waitFor(() => {
      expect(screen.getByText('Unauthorized')).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: /Clusters/ }));

    expect(screen.getByRole('button', { name: /Clusters/ })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    expect(screen.getByText('Unauthorized')).toBeInTheDocument();
  });

  it('disables search while catalog queries are loading', async () => {
    renderCatalogPage(createCatalogPageTransport({ vmDelayMs: 250, clusterItems: [] }));

    const searchInput = screen.getByRole('textbox', { name: 'Filter catalog by keyword' });
    expect(searchInput).toBeDisabled();

    await waitFor(() => {
      expect(screen.getByText(vmCatalogItem.title)).toBeInTheDocument();
    });
    expect(searchInput).toBeEnabled();
  });

  it('disables search when a catalog query is in error', async () => {
    const { user } = renderCatalogPage();

    await waitFor(() => {
      expect(screen.getByText('Unauthorized')).toBeInTheDocument();
    });

    expect(screen.getByRole('textbox', { name: 'Filter catalog by keyword' })).toBeDisabled();

    await user.click(screen.getByRole('button', { name: /Clusters/ }));
    expect(screen.getByRole('textbox', { name: 'Filter catalog by keyword' })).toBeDisabled();
  });

  it('shows catalog items for all types by default when queries succeed', async () => {
    renderCatalogPage(createCatalogPageTransport());

    await waitFor(() => {
      expect(screen.getByText(vmCatalogItem.title)).toBeInTheDocument();
      expect(screen.getByText(clusterCatalogItem.title)).toBeInTheDocument();
    });
  });

  it('filters to cluster catalog items when the cluster type toggle is selected', async () => {
    const { user } = renderCatalogPage(createCatalogPageTransport());

    await waitFor(() => {
      expect(screen.getByText(vmCatalogItem.title)).toBeInTheDocument();
      expect(screen.getByText(clusterCatalogItem.title)).toBeInTheDocument();
    });

    // With no type filter every type is shown; selecting Clusters narrows to clusters only.
    await user.click(screen.getByRole('button', { name: /Clusters/ }));

    await waitFor(() => {
      expect(screen.getByText(clusterCatalogItem.title)).toBeInTheDocument();
    });
    expect(screen.queryByText(vmCatalogItem.title)).not.toBeInTheDocument();
  });

  it('shows an error from a failed catalog type alongside items that loaded', async () => {
    renderCatalogPage(
      createCatalogPageTransport({
        clusterError: Object.assign(new Error(), { name: 'UnauthorizedError' }),
      }),
    );

    await waitFor(() => {
      expect(screen.getByText('Unauthorized')).toBeInTheDocument();
    });
    // VMs loaded successfully, so their items stay visible next to the error.
    expect(screen.getByText(vmCatalogItem.title)).toBeInTheDocument();
    expect(screen.queryByText(clusterCatalogItem.title)).not.toBeInTheDocument();
  });

  it('shows a generic section error for non-401 failures', async () => {
    renderCatalogPage(
      createCatalogPageTransport({ vmError: new Error('Catalog service unavailable') }),
    );

    await waitFor(() => {
      expect(screen.getByText('Catalog service unavailable')).toBeInTheDocument();
    });

    expect(screen.queryByText('Unauthorized')).not.toBeInTheDocument();
    // Clusters and bare metal loaded successfully, so search stays enabled despite the VM error.
    expect(screen.getByRole('textbox', { name: 'Filter catalog by keyword' })).toBeEnabled();
  });

  it('shows an empty state when no catalog items are returned', async () => {
    renderCatalogPage(createCatalogPageTransport({ vmItems: [], clusterItems: [], bmItems: [] }));

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: 'No catalog items found', level: 2 }),
      ).toBeInTheDocument();
    });

    expect(screen.getByText('No published catalog items are available yet.')).toBeInTheDocument();
  });

  it('filters catalog items by the search keyword', async () => {
    const secondVmItem: ComputeInstanceCatalogItem = {
      ...vmCatalogItem,
      id: 'catalog-fedora-40',
      metadata: {
        $typeName: 'osac.public.v1.Metadata',
        displayName: '',
        description: '',
        name: 'catalog-fedora-40',
        annotations: {},
        creator: 'foo',
        labels: {},
        project: 'foo',
        tenant: 'foo',
        version: 1,
      },
      title: 'Fedora 40 catalog',
      description: 'Fedora 40 workstation image',
    };

    const { user } = renderCatalogPage(
      createCatalogPageTransport({ vmItems: [vmCatalogItem, secondVmItem], clusterItems: [] }),
    );

    await waitFor(() => {
      expect(screen.getByText(vmCatalogItem.title)).toBeInTheDocument();
      expect(screen.getByText(secondVmItem.title)).toBeInTheDocument();
    });

    await user.type(screen.getByRole('textbox', { name: 'Filter catalog by keyword' }), 'fedora');

    await waitFor(() => {
      expect(screen.getByText(secondVmItem.title)).toBeInTheDocument();
    });
    expect(screen.queryByText(vmCatalogItem.title)).not.toBeInTheDocument();
  });

  it('shows a search-specific empty state when the filter matches nothing', async () => {
    const { user } = renderCatalogPage(createCatalogPageTransport({ clusterItems: [] }));

    await waitFor(() => {
      expect(screen.getByText(vmCatalogItem.title)).toBeInTheDocument();
    });

    await user.type(
      screen.getByRole('textbox', { name: 'Filter catalog by keyword' }),
      'no-such-catalog-item',
    );

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: 'No catalog items found', level: 2 }),
      ).toBeInTheDocument();
    });
    expect(screen.getByText('No catalog items match your filters.')).toBeInTheDocument();
  });

  it('navigates to cluster create from the catalog item detail page', async () => {
    const { user } = renderCatalogPageWithDetailRoutes();

    await waitFor(() => {
      expect(screen.getByText(clusterCatalogItem.title)).toBeInTheDocument();
    });

    await user.click(
      screen.getByRole('button', {
        name: `Open catalog item details for ${clusterCatalogItem.title}`,
      }),
    );

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: clusterCatalogItem.title, level: 1 }),
      ).toBeInTheDocument();
    });
    await user.click(await screen.findByRole('button', { name: 'Create cluster' }));

    await waitFor(() => {
      expect(screen.getByText('Create cluster page')).toBeInTheDocument();
    });
  });

  it('navigates to VM create from the catalog item detail page', async () => {
    const { user } = renderCatalogPageWithDetailRoutes();

    await waitFor(() => {
      expect(screen.getByText(vmCatalogItem.title)).toBeInTheDocument();
    });

    await user.click(
      screen.getByRole('button', {
        name: `Open catalog item details for ${vmCatalogItem.title}`,
      }),
    );

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: vmCatalogItem.title, level: 1 }),
      ).toBeInTheDocument();
    });

    await user.click(await screen.findByRole('button', { name: 'Create virtual machine' }));

    await waitFor(() => {
      expect(screen.getByText('Create virtual machine page')).toBeInTheDocument();
    });
  });
});
