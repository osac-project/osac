import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  Architecture,
  type DiskImage,
  DiskImageLifecycle,
  DiskImageSchema,
  DiskImages,
  type DiskImagesListResponse,
  DiskImagesListResponseSchema,
} from '@osac/types';
import { Tenants, type TenantsListResponse, TenantsListResponseSchema } from '@osac/types/private';
import { getViewTypePrefKey } from '@osac/ui-components/hooks/use-view-type';
import { mockQueryResult } from '@osac/ui-components/test-utils/query';

import DiskImageListPage from './DiskImageListPage';
import {
  DISK_IMAGE_NON_OBSOLETE_FILTER,
  diskImageListRequestFilter,
} from '../../api/v1/disk-image';
import { matchesDiskImageLifecycleFilter } from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.mock('../../api/use-resource', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/use-resource')>();
  return { ...actual, useListResource: vi.fn() };
});

const { useListResource } = await import('../../api/use-resource');

const { useListResource: useListResourceActual } =
  await vi.importActual<typeof import('../../api/use-resource')>('../../api/use-resource');

const emptyTenantsListResponse = (): TenantsListResponse =>
  create(TenantsListResponseSchema, { items: [], size: 0, total: 0 });

const makeDiskImage = (
  id: string,
  lifecycle: DiskImageLifecycle = DiskImageLifecycle.AVAILABLE,
): DiskImage =>
  create(DiskImageSchema, {
    id,
    metadata: {
      name: `disk-image-${id}`,
      creationTimestamp: { seconds: BigInt(1717000000), nanos: 0 },
    },
    spec: {
      sourceRef: `quay.io/example/${id}:latest`,
      architecture: [Architecture.AMD64],
      lifecycle,
    },
  });

const makeDiskImagesListResponse = (
  items: DiskImage[],
  total = items.length,
): DiskImagesListResponse =>
  create(DiskImagesListResponseSchema, {
    items,
    size: items.length,
    total,
  });

const mockDiskImagesListResource = (items: DiskImage[]) => {
  vi.mocked(useListResource).mockImplementation((service) => {
    if (service === Tenants) {
      return mockQueryResult<TenantsListResponse>({
        data: emptyTenantsListResponse(),
        isLoading: false,
        error: null,
      });
    }

    return mockQueryResult<DiskImagesListResponse>({
      data: makeDiskImagesListResponse(items),
      isLoading: false,
      error: null,
    });
  });
};

const getDiskImageListRequest = (): { filter?: string } | undefined => {
  const listCalls = vi
    .mocked(useListResource)
    .mock.calls.filter(([service]) => service === DiskImages);
  return listCalls.at(-1)?.[1] as { filter?: string } | undefined;
};

const renderPage = () => renderWithProviders(<DiskImageListPage />);

const renderPageWithCreateRoute = () =>
  renderWithProviders(
    <Routes>
      <Route path="/admin/infrastructure/disk-images" element={<DiskImageListPage />} />
      <Route
        path="/admin/infrastructure/disk-images/create"
        element={<h1>Create disk image page</h1>}
      />
    </Routes>,
    { routerEntries: ['/admin/infrastructure/disk-images'] },
  );

describe('DiskImageListPage', () => {
  beforeEach(() => {
    localStorage.clear();
    mockDiskImagesListResource([]);
  });

  afterEach(() => {
    vi.mocked(useListResource).mockReset();
  });

  it('sends the default non-obsolete filter when no user filters are set', () => {
    mockDiskImagesListResource([]);

    renderPage();

    expect(getDiskImageListRequest()?.filter).toBe(DISK_IMAGE_NON_OBSOLETE_FILTER);
  });

  it('renders rows for populated data', () => {
    mockDiskImagesListResource([makeDiskImage('a')]);

    renderPage();

    expect(screen.getByRole('heading', { name: 'Disk images' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'disk-image-a' })).toBeInTheDocument();
  });

  it('shows a loading spinner while the query is in flight', () => {
    vi.mocked(useListResource).mockImplementation((service) => {
      if (service === Tenants) {
        return mockQueryResult<TenantsListResponse>({
          data: emptyTenantsListResponse(),
          isLoading: false,
          error: null,
        });
      }
      return mockQueryResult<DiskImagesListResponse>({
        data: undefined,
        isPending: true,
        error: null,
      });
    });

    renderPage();

    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });

  it('shows the empty state when no disk images are returned', async () => {
    mockDiskImagesListResource([]);

    renderPage();

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: 'No disk images found', level: 2 }),
      ).toBeInTheDocument();
    });
    expect(screen.getByText('No disk images are available yet.')).toBeInTheDocument();
    expect(screen.queryByRole('grid', { name: 'Disk images' })).not.toBeInTheDocument();
  });

  it('shows a filter-specific empty state when filters match nothing', async () => {
    mockDiskImagesListResource([]);

    renderWithProviders(<DiskImageListPage />, {
      routerEntries: ['/admin/infrastructure/disk-images?search=missing'],
    });

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: 'No disk images match your filters', level: 2 }),
      ).toBeInTheDocument();
    });
    expect(
      screen.getByText(
        /Try a different guest OS family, architecture, lifecycle, scope, or search term/,
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Clear all filters' })).toBeInTheDocument();
  });

  it('renders the list and card view switcher', () => {
    mockDiskImagesListResource([]);

    renderPage();

    expect(screen.getByRole('button', { name: 'Card view' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'List view' })).toBeInTheDocument();
  });

  it('renders disk images as cards by default', async () => {
    mockDiskImagesListResource([makeDiskImage('a')]);

    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'disk-image-a' })).toBeInTheDocument();
    });
    expect(screen.getByText('Guest OS family')).toBeInTheDocument();
    expect(screen.queryByRole('grid', { name: 'Disk images' })).not.toBeInTheDocument();
  });

  it('renders the table when list view is selected', async () => {
    localStorage.setItem(getViewTypePrefKey('disk-images-view'), 'list');
    mockDiskImagesListResource([makeDiskImage('a')]);

    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('grid', { name: 'Disk images' })).toBeInTheDocument();
    });
    expect(screen.getByRole('link', { name: 'disk-image-a' })).toBeInTheDocument();
  });

  it('clears URL filters from the empty state', async () => {
    mockDiskImagesListResource([]);

    const { user } = renderWithProviders(<DiskImageListPage />, {
      routerEntries: ['/admin/infrastructure/disk-images?search=missing&guestOsFamily=linux'],
    });

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Clear all filters' })).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Clear all filters' }));

    expect(getDiskImageListRequest()).toEqual({
      filter: diskImageListRequestFilter({
        search: '',
        guestOsFamily: undefined,
        architecture: [],
        lifecycle: [],
        showObsolete: false,
        scope: undefined,
      }),
    });
  });

  it('uses the page-level error state when the query fails', () => {
    vi.mocked(useListResource).mockImplementation((service) => {
      if (service === Tenants) {
        return mockQueryResult<TenantsListResponse>({
          data: emptyTenantsListResponse(),
          isLoading: false,
          error: null,
        });
      }
      return mockQueryResult<DiskImagesListResponse>({
        data: undefined,
        isLoading: false,
        error: new Error('Disk images unavailable'),
      });
    });

    renderPage();

    expect(screen.getByText('An error occurred')).toBeInTheDocument();
    expect(screen.getByText('Disk images unavailable')).toBeInTheDocument();
  });

  it('navigates to the create route when the create button is clicked', async () => {
    mockDiskImagesListResource([]);

    const { user } = renderPageWithCreateRoute();
    await user.click(screen.getByRole('link', { name: 'Create disk image' }));

    expect(screen.getByRole('heading', { name: 'Create disk image page' })).toBeInTheDocument();
  });

  it('composes a name-search filter and re-queries', async () => {
    mockDiskImagesListResource([]);
    const { user } = renderPage();

    await user.type(screen.getByRole('textbox', { name: 'Search disk images' }), 'fedora');

    expect(getDiskImageListRequest()).toEqual({
      filter: diskImageListRequestFilter({
        search: 'fedora',
        guestOsFamily: undefined,
        architecture: [],
        lifecycle: [],
        showObsolete: false,
        scope: undefined,
      }),
    });
  });

  it('composes a guest OS family filter and re-queries', async () => {
    mockDiskImagesListResource([]);
    const { user } = renderPage();

    await user.click(
      screen.getByRole('button', { name: 'Guest OS family: All guest OS families' }),
    );
    await user.click(screen.getByRole('option', { name: 'Linux' }));

    expect(getDiskImageListRequest()).toEqual({
      filter: diskImageListRequestFilter({
        search: '',
        guestOsFamily: 1,
        architecture: [],
        lifecycle: [],
        showObsolete: false,
        scope: undefined,
      }),
    });
  });

  it('composes an architecture filter and re-queries', async () => {
    mockDiskImagesListResource([]);
    const { user } = renderPage();

    await user.click(screen.getByRole('button', { name: 'Architecture' }));
    await user.click(screen.getByRole('checkbox', { name: 'amd64' }));

    expect(getDiskImageListRequest()).toEqual({
      filter: diskImageListRequestFilter({
        search: '',
        guestOsFamily: undefined,
        architecture: [Architecture.AMD64],
        lifecycle: [],
        showObsolete: false,
        scope: undefined,
      }),
    });
  });

  it('composes a lifecycle filter and re-queries', async () => {
    mockDiskImagesListResource([]);
    const { user } = renderPage();

    await user.click(screen.getByRole('button', { name: 'Lifecycle' }));
    await user.click(screen.getByRole('checkbox', { name: 'Available' }));

    expect(getDiskImageListRequest()).toEqual({
      filter: diskImageListRequestFilter({
        search: '',
        guestOsFamily: undefined,
        architecture: [],
        lifecycle: [DiskImageLifecycle.AVAILABLE],
        showObsolete: false,
        scope: undefined,
      }),
    });
  });

  it('composes a scope filter and re-queries', async () => {
    mockDiskImagesListResource([]);
    const { user } = renderPage();

    await user.click(screen.getByRole('button', { name: 'Scope: All scopes' }));
    await user.click(screen.getByRole('option', { name: 'Global' }));

    expect(getDiskImageListRequest()).toEqual({
      filter: diskImageListRequestFilter({
        search: '',
        guestOsFamily: undefined,
        architecture: [],
        lifecycle: [],
        showObsolete: false,
        scope: 'global',
      }),
    });
  });

  it('composes a show-obsolete filter and re-queries', async () => {
    mockDiskImagesListResource([]);
    const { user } = renderPage();

    await user.click(screen.getByRole('checkbox', { name: 'Show obsolete' }));

    expect(getDiskImageListRequest()).toEqual({
      filter: diskImageListRequestFilter({
        search: '',
        guestOsFamily: undefined,
        architecture: [],
        lifecycle: [],
        showObsolete: true,
        scope: undefined,
      }),
    });
  });

  it('hides OBSOLETE disk images by default and reveals them when show-obsolete is toggled', async () => {
    vi.mocked(useListResource).mockImplementation(useListResourceActual);
    const items = [
      makeDiskImage('available-1', DiskImageLifecycle.AVAILABLE),
      makeDiskImage('obsolete-1', DiskImageLifecycle.OBSOLETE),
    ];
    const capturedDiskImageListFilters: (string | undefined)[] = [];
    const obsoleteLifecycleInclusion = `this.spec.lifecycle == ${DiskImageLifecycle.OBSOLETE}`;
    const availableOnlyLifecyclePredicate = `this.spec.lifecycle == ${DiskImageLifecycle.AVAILABLE}`;

    const { user } = renderWithProviders(<DiskImageListPage />, {
      apiFixtures: { diskImages: items },
      transportOverrides: {
        onDiskImageList: (req) => {
          capturedDiskImageListFilters.push(req.filter);
          const filtered = items.filter((item) =>
            matchesDiskImageLifecycleFilter(req.filter, item.spec?.lifecycle),
          );
          return makeDiskImagesListResponse(filtered);
        },
      },
    });

    await screen.findByText('disk-image-available-1');
    expect(capturedDiskImageListFilters.at(-1)).toBe(DISK_IMAGE_NON_OBSOLETE_FILTER);
    expect(screen.queryByText('disk-image-obsolete-1')).not.toBeInTheDocument();

    await user.click(screen.getByRole('checkbox', { name: 'Show obsolete' }));

    await waitFor(() => {
      const latestFilter = capturedDiskImageListFilters.at(-1);
      expect(latestFilter).toBeDefined();
      expect(latestFilter).toContain(obsoleteLifecycleInclusion);
      expect(latestFilter).not.toContain(DISK_IMAGE_NON_OBSOLETE_FILTER);
      expect(latestFilter).not.toBe(availableOnlyLifecyclePredicate);
    });
    await screen.findByText('disk-image-obsolete-1');
    expect(screen.getByText('disk-image-available-1')).toBeInTheDocument();
  });
});
