import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { Architecture, type DiskImage, DiskImageLifecycle, DiskImageSchema } from '@osac/types';
import { getViewTypePrefKey } from '@osac/ui-components/hooks/use-view-type';
import { mockQueryResult } from '@osac/ui-components/test-utils/query';

import DiskImageListPage from './DiskImageListPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.mock('@osac/ui-components/api/v1/disk-image', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@osac/ui-components/api/v1/disk-image')>();
  return { ...actual, useDiskImages: vi.fn() };
});

const { buildDiskImageListFilter, useDiskImages } =
  await import('@osac/ui-components/api/v1/disk-image');
const { useDiskImages: useDiskImagesActual } = await vi.importActual<
  typeof import('@osac/ui-components/api/v1/disk-image')
>('@osac/ui-components/api/v1/disk-image');

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
  });

  afterEach(() => {
    vi.mocked(useDiskImages).mockReset();
  });

  it('renders rows for populated data', () => {
    vi.mocked(useDiskImages).mockReturnValue(
      mockQueryResult<DiskImage[]>({ data: [makeDiskImage('a')] }),
    );

    renderPage();

    expect(screen.getByRole('heading', { name: 'Disk images' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'disk-image-a' })).toBeInTheDocument();
  });

  it('shows a loading spinner while the query is in flight', () => {
    vi.mocked(useDiskImages).mockReturnValue(
      mockQueryResult<DiskImage[]>({ data: undefined, isLoading: true }),
    );

    renderPage();

    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });

  it('shows the empty state when no disk images are returned', async () => {
    vi.mocked(useDiskImages).mockReturnValue(mockQueryResult<DiskImage[]>({ data: [] }));

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
    vi.mocked(useDiskImages).mockReturnValue(mockQueryResult<DiskImage[]>({ data: [] }));

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
    vi.mocked(useDiskImages).mockReturnValue(mockQueryResult<DiskImage[]>({ data: [] }));

    renderPage();

    expect(screen.getByRole('button', { name: 'Card view' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'List view' })).toBeInTheDocument();
  });

  it('renders disk images as cards by default', async () => {
    vi.mocked(useDiskImages).mockReturnValue(
      mockQueryResult<DiskImage[]>({ data: [makeDiskImage('a')] }),
    );

    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'disk-image-a' })).toBeInTheDocument();
    });
    expect(screen.getByText('Guest OS family')).toBeInTheDocument();
    expect(screen.queryByRole('grid', { name: 'Disk images' })).not.toBeInTheDocument();
  });

  it('renders the table when list view is selected', async () => {
    localStorage.setItem(getViewTypePrefKey('disk-images-view'), 'list');
    vi.mocked(useDiskImages).mockReturnValue(
      mockQueryResult<DiskImage[]>({ data: [makeDiskImage('a')] }),
    );

    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('grid', { name: 'Disk images' })).toBeInTheDocument();
    });
    expect(screen.getByRole('link', { name: 'disk-image-a' })).toBeInTheDocument();
  });

  it('clears URL filters from the empty state', async () => {
    vi.mocked(useDiskImages).mockReturnValue(mockQueryResult<DiskImage[]>({ data: [] }));

    const { user } = renderWithProviders(<DiskImageListPage />, {
      routerEntries: ['/admin/infrastructure/disk-images?search=missing&guestOsFamily=linux'],
    });

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Clear all filters' })).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Clear all filters' }));

    expect(useDiskImages).toHaveBeenLastCalledWith({
      filter: buildDiskImageListFilter({
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
    vi.mocked(useDiskImages).mockReturnValue(
      mockQueryResult<DiskImage[]>({ data: [], error: new Error('Disk images unavailable') }),
    );

    renderPage();

    expect(screen.getByText('An error occurred')).toBeInTheDocument();
    expect(screen.getByText('Disk images unavailable')).toBeInTheDocument();
  });

  it('navigates to the create route when the create button is clicked', async () => {
    vi.mocked(useDiskImages).mockReturnValue(mockQueryResult<DiskImage[]>({ data: [] }));

    const { user } = renderPageWithCreateRoute();
    await user.click(screen.getByRole('link', { name: 'Create disk image' }));

    expect(screen.getByRole('heading', { name: 'Create disk image page' })).toBeInTheDocument();
  });

  it('composes a name-search filter and re-queries', async () => {
    vi.mocked(useDiskImages).mockReturnValue(mockQueryResult<DiskImage[]>({ data: [] }));
    const { user } = renderPage();

    await user.type(screen.getByRole('textbox', { name: 'Search disk images' }), 'fedora');

    expect(useDiskImages).toHaveBeenLastCalledWith({
      filter: buildDiskImageListFilter({
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
    vi.mocked(useDiskImages).mockReturnValue(mockQueryResult<DiskImage[]>({ data: [] }));
    const { user } = renderPage();

    await user.click(
      screen.getByRole('button', { name: 'Guest OS family: All guest OS families' }),
    );
    await user.click(screen.getByRole('option', { name: 'Linux' }));

    expect(useDiskImages).toHaveBeenLastCalledWith({
      filter: buildDiskImageListFilter({
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
    vi.mocked(useDiskImages).mockReturnValue(mockQueryResult<DiskImage[]>({ data: [] }));
    const { user } = renderPage();

    await user.click(screen.getByRole('button', { name: 'Architecture' }));
    await user.click(screen.getByRole('checkbox', { name: 'amd64' }));

    expect(useDiskImages).toHaveBeenLastCalledWith({
      filter: buildDiskImageListFilter({
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
    vi.mocked(useDiskImages).mockReturnValue(mockQueryResult<DiskImage[]>({ data: [] }));
    const { user } = renderPage();

    await user.click(screen.getByRole('button', { name: 'Lifecycle' }));
    await user.click(screen.getByRole('checkbox', { name: 'Available' }));

    expect(useDiskImages).toHaveBeenLastCalledWith({
      filter: buildDiskImageListFilter({
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
    vi.mocked(useDiskImages).mockReturnValue(mockQueryResult<DiskImage[]>({ data: [] }));
    const { user } = renderPage();

    await user.click(screen.getByRole('button', { name: 'Scope: All scopes' }));
    await user.click(screen.getByRole('option', { name: 'Global' }));

    expect(useDiskImages).toHaveBeenLastCalledWith({
      filter: buildDiskImageListFilter({
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
    vi.mocked(useDiskImages).mockReturnValue(mockQueryResult<DiskImage[]>({ data: [] }));
    const { user } = renderPage();

    await user.click(screen.getByRole('checkbox', { name: 'Show obsolete' }));

    expect(useDiskImages).toHaveBeenLastCalledWith({
      filter: buildDiskImageListFilter({
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
    vi.mocked(useDiskImages).mockImplementation(useDiskImagesActual);
    const items = [
      makeDiskImage('available-1', DiskImageLifecycle.AVAILABLE),
      makeDiskImage('obsolete-1', DiskImageLifecycle.OBSOLETE),
    ];

    const { user } = renderWithProviders(<DiskImageListPage />, {
      apiFixtures: { diskImages: items },
    });

    await screen.findByText('disk-image-available-1');
    expect(screen.queryByText('disk-image-obsolete-1')).not.toBeInTheDocument();

    await user.click(screen.getByRole('checkbox', { name: 'Show obsolete' }));

    await screen.findByText('disk-image-obsolete-1');
    expect(screen.getByText('disk-image-available-1')).toBeInTheDocument();
  });
});
