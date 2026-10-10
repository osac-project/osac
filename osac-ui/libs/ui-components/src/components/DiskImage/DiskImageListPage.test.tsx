import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { Architecture, type DiskImage, DiskImageLifecycle, DiskImageSchema } from '@osac/types';
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

  it('shows the empty state when no disk images are returned', () => {
    vi.mocked(useDiskImages).mockReturnValue(mockQueryResult<DiskImage[]>({ data: [] }));

    renderPage();

    expect(screen.getByText('No disk images yet.')).toBeInTheDocument();
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
