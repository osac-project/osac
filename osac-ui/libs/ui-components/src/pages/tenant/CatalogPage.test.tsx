import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  BareMetalInstanceCatalogItem,
  BareMetalInstanceCatalogItems,
  ClusterCatalogItem,
  ClusterCatalogItems,
  ClusterTemplateReferenceSchema,
  ComputeInstanceCatalogItem,
  ComputeInstanceCatalogItems,
  ComputeInstanceTemplateReferenceSchema,
  Metadata,
} from '@osac/types';

import CatalogPage from './CatalogPage';
import {
  type CatalogListFilterableItem,
  catalogItemMatchesListFilter,
} from '../../api/v1/catalog-item-list-filter';
import { CATALOG_ITEMS_VIEW_KEY } from '../../components/catalog/CatalogItemListSection';
import { CatalogItemDetailPage } from '../../components/catalog/details/CatalogItemDetailPage.tsx';
import { getViewTypePrefKey } from '../../components/Primitives/ViewSwitcher';
import { wrapWithAuthInterceptor } from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

const catalogItemsViewPrefKey = getViewTypePrefKey(CATALOG_ITEMS_VIEW_KEY);

vi.mock('@osac/ui-components/hooks/use-session.tsx', () => ({
  useSession: vi.fn(() => ({ role: 'admin', username: 'test-user', tenantId: 'test-tenant' })),
}));

const vmCatalogMetaData: Metadata = {
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
};

const vmCatalogItem: ComputeInstanceCatalogItem = {
  $typeName: 'osac.public.v1.ComputeInstanceCatalogItem',
  id: 'catalog-rhel-9',
  metadata: vmCatalogMetaData,
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

const toCatalogListResponse = <T extends CatalogListFilterableItem>(
  allItems: T[],
  req: { filter?: string; limit?: number },
) => {
  const filteredItems = allItems.filter((item) => catalogItemMatchesListFilter(item, req.filter));
  const items = req.limit === 0 ? [] : filteredItems;

  return {
    items,
    size: items.length,
    total: allItems.length,
  };
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
        list: async (req) => {
          if (vmDelayMs) {
            await delay(vmDelayMs);
          }
          if (vmError) {
            throw toConnectError(vmError);
          }
          return toCatalogListResponse(vmItems, req);
        },
        get: (req) => ({
          object: vmItems.find((i) => i.id === req.id),
        }),
      });

      router.service(ClusterCatalogItems, {
        list: async (req) => {
          if (clusterDelayMs) {
            await delay(clusterDelayMs);
          }
          if (clusterError) {
            throw toConnectError(clusterError);
          }
          return toCatalogListResponse(clusterItems, req);
        },
        get: (req) => ({
          object: clusterItems.find((i) => i.id === req.id),
        }),
      });

      router.service(BareMetalInstanceCatalogItems, {
        list: async (req) => {
          if (bmDelayMs) {
            await delay(bmDelayMs);
          }
          if (bmError) {
            throw toConnectError(bmError);
          }
          return toCatalogListResponse(bmItems, req);
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
      <Route
        path="/bare-metal/create/:catalogItemId?"
        element={<div>Create bare metal page</div>}
      />
      <Route path="/clusters/create/:catalogItemId?" element={<div>Create cluster page</div>} />
      <Route path="/vms/create/:catalogItemId?" element={<div>Create virtual machine page</div>} />
    </Routes>,
    { transport, routerEntries: ['/catalog'] },
  );

describe('CatalogPage', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('keeps type filter toggles in the DOM when catalog queries return 401', async () => {
    renderCatalogPage();

    await waitFor(() => {
      expect(screen.getByText('Unauthorized')).toBeInTheDocument();
    });

    expect(screen.getByText('Global marketplace').closest('.pf-v6-c-label')).not.toBeNull();
    expect(
      screen.getByRole('group', { name: 'Filter catalog by resource type' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Filter catalog by publication status' }),
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
      expect(screen.getByText(vmCatalogItem.metadata?.name ?? '')).toBeInTheDocument();
    });
    expect(searchInput).toBeEnabled();
  });

  it('disables search when a catalog query is in error', async () => {
    renderCatalogPage();

    await waitFor(() => {
      expect(screen.getByText('Unauthorized')).toBeInTheDocument();
    });

    expect(screen.getByRole('textbox', { name: 'Filter catalog by keyword' })).toBeDisabled();
  });

  it('shows catalog items for all types by default when queries succeed', async () => {
    renderCatalogPage(createCatalogPageTransport());

    await waitFor(() => {
      expect(screen.getByText(vmCatalogItem.metadata?.name ?? '')).toBeInTheDocument();
      expect(screen.getByText(clusterCatalogItem.metadata?.name ?? '')).toBeInTheDocument();
    });
  });

  it('filters to cluster catalog items when the cluster type toggle is selected', async () => {
    const { user } = renderCatalogPage(createCatalogPageTransport());

    await waitFor(() => {
      expect(screen.getByText(vmCatalogItem.metadata?.name ?? '')).toBeInTheDocument();
      expect(screen.getByText(clusterCatalogItem.metadata?.name ?? '')).toBeInTheDocument();
    });

    // With no type filter every type with items is shown as selected; clicking a type deselects it.
    await user.click(screen.getByRole('button', { name: /Virtual Machines/ }));

    await waitFor(() => {
      expect(screen.getByText(clusterCatalogItem.metadata?.name ?? '')).toBeInTheDocument();
    });
    expect(screen.queryByText(vmCatalogItem.metadata?.name ?? '')).not.toBeInTheDocument();
  });

  it('filters catalog items by publication status', async () => {
    const unpublishedVmItem: ComputeInstanceCatalogItem = {
      ...vmCatalogItem,
      id: 'catalog-rhel-8-draft',
      metadata: {
        ...vmCatalogMetaData,
        name: 'catalog-rhel-8-draft',
      },
      title: 'RHEL 8 draft',
      description: 'Unpublished RHEL 8 image',
      published: false,
    };

    const { user } = renderCatalogPage(
      createCatalogPageTransport({
        vmItems: [vmCatalogItem, unpublishedVmItem],
        clusterItems: [],
      }),
    );

    await waitFor(() => {
      expect(screen.getByText(vmCatalogItem.metadata?.name ?? '')).toBeInTheDocument();
      expect(screen.getByText(unpublishedVmItem.metadata?.name ?? '')).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Filter catalog by publication status' }));
    await user.click(screen.getByRole('option', { name: 'Unpublished' }));

    await waitFor(() => {
      expect(screen.getByText(unpublishedVmItem.metadata?.name ?? '')).toBeInTheDocument();
    });
    expect(screen.queryByText(vmCatalogItem.metadata?.name ?? '')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Filter catalog by publication status' }));
    await user.click(screen.getByRole('option', { name: 'Published' }));

    await waitFor(() => {
      expect(screen.getByText(vmCatalogItem.metadata?.name ?? '')).toBeInTheDocument();
    });
    expect(screen.queryByText(unpublishedVmItem.metadata?.name ?? '')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Filter catalog by publication status' }));
    await user.click(screen.getByRole('option', { name: 'All publish states' }));

    await waitFor(() => {
      expect(screen.getByText(vmCatalogItem.metadata?.name ?? '')).toBeInTheDocument();
      expect(screen.getByText(unpublishedVmItem.metadata?.name ?? '')).toBeInTheDocument();
    });
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
    expect(screen.getByText(vmCatalogItem.metadata?.name ?? '')).toBeInTheDocument();
    expect(screen.queryByText(clusterCatalogItem.metadata?.name ?? '')).not.toBeInTheDocument();
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

    expect(screen.getByText('No catalog items are available yet.')).toBeInTheDocument();
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

    renderWithProviders(<CatalogPage />, {
      transport: createCatalogPageTransport({
        vmItems: [vmCatalogItem, secondVmItem],
        clusterItems: [],
      }),
      routerEntries: ['/catalog?search=fedora'],
    });

    await waitFor(() => {
      expect(screen.getByText(secondVmItem.metadata?.name ?? '')).toBeInTheDocument();
    });
    expect(screen.queryByText(vmCatalogItem.metadata?.name ?? '')).not.toBeInTheDocument();
  });

  it('shows a search-specific empty state when the filter matches nothing', async () => {
    renderWithProviders(<CatalogPage />, {
      transport: createCatalogPageTransport({ clusterItems: [] }),
      routerEntries: ['/catalog?search=no-such-catalog-item'],
    });

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: 'No catalog items match your filters', level: 2 }),
      ).toBeInTheDocument();
    });
    expect(
      screen.getByText('Try a different service, publish status, tenant, or search term.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Clear all filters' })).toBeInTheDocument();
  });

  it('clears type, publish, and search filters together without dropping other params', async () => {
    localStorage.setItem(catalogItemsViewPrefKey, 'list');

    const { user } = renderWithProviders(<CatalogPage />, {
      transport: createCatalogPageTransport(),
      routerEntries: ['/catalog?types=cluster&published=unpublished&search=no-such-item'],
    });

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: 'No catalog items match your filters', level: 2 }),
      ).toBeInTheDocument();
    });

    expect(screen.getByRole('button', { name: /Clusters/ })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    expect(
      screen.getByRole('button', { name: 'Filter catalog by publication status' }),
    ).toHaveTextContent('Unpublished');
    expect(screen.getByRole('textbox', { name: 'Filter catalog by keyword' })).toHaveValue(
      'no-such-item',
    );

    await user.click(screen.getByRole('button', { name: 'Clear all filters' }));

    await waitFor(() => {
      expect(screen.getByText(vmCatalogItem.metadata?.name || 'N/A')).toBeInTheDocument();
      expect(screen.getByText(clusterCatalogItem.metadata?.name || 'N/A')).toBeInTheDocument();
    });

    expect(screen.getByRole('button', { name: /Clusters/ })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    expect(screen.getByRole('button', { name: /Virtual Machines/ })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    expect(
      screen.getByRole('button', { name: 'Filter catalog by publication status' }),
    ).toHaveTextContent('All publish states');
    expect(screen.getByRole('textbox', { name: 'Filter catalog by keyword' })).toHaveValue('');
    // View preference is stored separately from URL filters, so list view must survive the clear.
    expect(screen.getByRole('grid', { name: 'Catalog items' })).toBeInTheDocument();
  });

  it('navigates to cluster create from the catalog item detail page', async () => {
    const { user } = renderCatalogPageWithDetailRoutes();

    await waitFor(() => {
      expect(screen.getByText(clusterCatalogItem.metadata?.name ?? '')).toBeInTheDocument();
    });

    await user.click(screen.getByRole('link', { name: clusterCatalogItem.metadata?.name ?? '' }));

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: clusterCatalogItem.metadata?.name, level: 1 }),
      ).toBeInTheDocument();
    });
    await user.click(await screen.findByRole('button', { name: 'Launch instance' }));

    await waitFor(() => {
      expect(screen.getByText('Create cluster page')).toBeInTheDocument();
    });
  });

  it('navigates to VM create from the catalog item detail page', async () => {
    const { user } = renderCatalogPageWithDetailRoutes();

    await waitFor(() => {
      expect(screen.getByText(vmCatalogItem.metadata?.name ?? '')).toBeInTheDocument();
    });

    await user.click(screen.getByRole('link', { name: vmCatalogItem.metadata?.name ?? '' }));

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: vmCatalogItem.metadata?.name, level: 1 }),
      ).toBeInTheDocument();
    });

    await user.click(await screen.findByRole('button', { name: 'Launch instance' }));

    await waitFor(() => {
      expect(screen.getByText('Create virtual machine page')).toBeInTheDocument();
    });
  });

  it('switches to a list table that links to catalog item details', async () => {
    const { user } = renderCatalogPage(createCatalogPageTransport());

    await waitFor(() => {
      expect(screen.getByText(vmCatalogItem.metadata?.name ?? '')).toBeInTheDocument();
    });

    expect(screen.queryByRole('grid')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'List view' }));

    expect(await screen.findByRole('grid', { name: 'Catalog items' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: vmCatalogItem.metadata?.name })).toHaveAttribute(
      'href',
      `/catalog/vm/${vmCatalogItem.id}`,
    );
    expect(screen.getByRole('link', { name: clusterCatalogItem.metadata?.name })).toHaveAttribute(
      'href',
      `/catalog/cluster/${clusterCatalogItem.id}`,
    );
  });
});
