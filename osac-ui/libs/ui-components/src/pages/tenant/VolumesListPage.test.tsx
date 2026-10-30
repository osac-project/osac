import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  type Volume,
  VolumeAccessMode,
  VolumeSchema,
  VolumeState,
  type VolumesListResponse,
  VolumesListResponseSchema,
} from '@osac/types';
import { buildVolumeListFilter } from '@osac/ui-components/components/Volume/utils';
import { mockQueryResult } from '@osac/ui-components/test-utils/query';

import { VolumesListPage } from './VolumesListPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) =>
  window.setTimeout(() => callback(0), 0),
);

const makeVolume = (id: string, name: string, state: VolumeState = VolumeState.AVAILABLE): Volume =>
  create(VolumeSchema, {
    id,
    metadata: {
      name,
      project: 'test-project',
      creationTimestamp: { seconds: BigInt(1717000000), nanos: 0 },
    },
    spec: {
      storageTier: 'standard',
      sizeGib: BigInt(50),
      accessMode: VolumeAccessMode.READ_WRITE_ONCE,
    },
    status: { state },
  });

const makeListResponse = (items: Volume[], total?: number): VolumesListResponse =>
  create(VolumesListResponseSchema, { items, total: total ?? items.length });

vi.mock('../../api/use-resource', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/use-resource')>();
  return { ...actual, useListResource: vi.fn() };
});

vi.mock('../../components/Page/ProjectFilter', () => ({
  __esModule: true,
  default: () => <div data-testid="project-filter">Project filter</div>,
}));

const { useListResource } = await import('../../api/use-resource');

const mockListResource = (items: Volume[], total?: number) => {
  vi.mocked(useListResource).mockImplementation((_service, request) => {
    const limit = request?.limit;
    if (limit === 0) {
      return mockQueryResult<VolumesListResponse>({
        data: makeListResponse([], total ?? items.length),
        isLoading: false,
        error: null,
      });
    }
    return mockQueryResult<VolumesListResponse>({
      data: makeListResponse(items, total ?? items.length),
      isLoading: false,
      error: null,
    });
  });
};

const renderPage = (routerEntries?: string[]) =>
  renderWithProviders(<VolumesListPage />, {
    session: { role: 'tenant-user' },
    routerEntries,
  });

const getVolumeListFilter = (): string | undefined => {
  const listCalls = vi.mocked(useListResource).mock.calls.filter(([, request]) => {
    const { limit } = request as { limit?: number };
    return limit !== 0;
  });
  const { filter } = (listCalls.at(-1)?.[1] ?? {}) as { filter?: string };
  return filter;
};

describe('VolumesListPage', () => {
  beforeEach(() => {
    localStorage.clear();
    mockListResource([makeVolume('v-1', 'web-vol')]);
  });

  it('renders the page title', async () => {
    renderPage();

    expect(await screen.findByRole('heading', { name: 'Volumes' })).toBeInTheDocument();
  });

  it('renders the volume table with data', async () => {
    renderPage();

    expect(await screen.findByRole('link', { name: 'web-vol' })).toBeInTheDocument();
    expect(screen.getByText('50 GiB')).toBeInTheDocument();
    expect(screen.getByText('Available')).toBeInTheDocument();
    expect(screen.getByText('1 volume')).toBeInTheDocument();
  });

  it('renders loading state', () => {
    vi.mocked(useListResource).mockReturnValue(
      mockQueryResult<VolumesListResponse>({
        data: undefined,
        isLoading: true,
        error: null,
      }),
    );

    renderPage();

    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });

  it('renders error state on API failure', async () => {
    vi.mocked(useListResource).mockReturnValue(
      mockQueryResult<VolumesListResponse>({
        data: undefined,
        isLoading: false,
        error: new Error('Network error'),
      }),
    );

    renderPage();

    expect(await screen.findByText('An error occurred')).toBeInTheDocument();
    expect(screen.getByText('Network error')).toBeInTheDocument();
  });

  it('shows an empty state when no volumes are returned', async () => {
    mockListResource([], 0);

    renderPage();

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: 'No volumes found', level: 2 }),
      ).toBeInTheDocument();
    });
    expect(screen.getByText('No volumes are available yet.')).toBeInTheDocument();
  });

  it('shows a filter-specific empty state when the search matches nothing', async () => {
    mockListResource([], 1);

    renderPage(['/?search=no-such-volume']);

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: 'No volumes match your filters', level: 2 }),
      ).toBeInTheDocument();
    });
    expect(screen.getByText('Try a different project, state, or search term.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Clear all filters' })).toBeInTheDocument();
  });

  it('renders the name search input', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByPlaceholderText('Search volumes')).toBeInTheDocument();
    });
  });

  it('renders the state filter toggle', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'All states' })).toBeInTheDocument();
    });
  });

  it('renders the project filter', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByTestId('project-filter')).toBeInTheDocument();
    });
  });

  it('renders the list and card view switcher', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Card view' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'List view' })).toBeInTheDocument();
    });
  });

  // TODO: re-enable when volume creation is supported after developer preview
  it('does not render the Create volume button (disabled for developer preview)', async () => {
    renderPage();

    await screen.findByRole('heading', { name: 'Volumes' });
    expect(screen.queryByRole('link', { name: /Create volume/i })).not.toBeInTheDocument();
  });

  it('does not pass a custom refetchInterval to useListResource', async () => {
    renderPage();

    await waitFor(() => {
      expect(vi.mocked(useListResource).mock.calls.length).toBeGreaterThan(0);
    });
    const options = vi.mocked(useListResource).mock.calls.find((call) => call[2])?.[2];
    expect(options).toBeUndefined();
  });

  describe('filters', () => {
    it('sends a CEL filter when filtering volumes by state', async () => {
      const { user } = renderPage();

      await screen.findByRole('link', { name: 'web-vol' });

      await user.click(screen.getByRole('button', { name: 'All states' }));
      await user.click(screen.getByRole('checkbox', { name: 'Failed' }));

      const expected = buildVolumeListFilter(undefined, [VolumeState.FAILED], '');

      await waitFor(() => {
        expect(getVolumeListFilter()).toBe(expected);
      });
    });

    it('sends a CEL filter when searching volumes by name', async () => {
      const { user } = renderPage();

      await screen.findByRole('link', { name: 'web-vol' });

      await user.type(screen.getByRole('textbox', { name: 'Search volumes' }), 'web');

      const expected = buildVolumeListFilter(undefined, [], 'web');

      await waitFor(() => {
        expect(getVolumeListFilter()).toBe(expected);
      });
    });

    it('applies state and search filters from URL search params', async () => {
      renderPage(['/?state=creating&search=db-vol']);

      await waitFor(() => {
        expect(screen.getByRole('textbox', { name: 'Search volumes' })).toHaveValue('db-vol');
        expect(screen.getByRole('button', { name: 'Creating' })).toBeInTheDocument();
      });

      const expected = buildVolumeListFilter(undefined, [VolumeState.CREATING], 'db-vol');

      await waitFor(() => {
        expect(getVolumeListFilter()).toBe(expected);
      });
    });

    it('clears search and state filters together', async () => {
      const { user } = renderPage(['/?search=no-such&state=available']);

      await waitFor(() => {
        expect(screen.getByRole('textbox', { name: 'Search volumes' })).toHaveValue('no-such');
        expect(screen.getByRole('button', { name: 'Available' })).toBeInTheDocument();
      });

      const clearFiltersButtons = await screen.findAllByRole('button', {
        name: 'Clear all filters',
      });
      await user.click(clearFiltersButtons[0]);

      await waitFor(() => {
        expect(screen.getByRole('textbox', { name: 'Search volumes' })).toHaveValue('');
      });
      await waitFor(() => {
        expect(getVolumeListFilter()).toBeUndefined();
      });

      expect(screen.getByRole('button', { name: 'All states' })).toBeInTheDocument();
    });
  });
});
