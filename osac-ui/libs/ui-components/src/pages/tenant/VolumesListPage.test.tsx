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
import { mockQueryResult } from '@osac/ui-components/test-utils/query';

import { VolumesListPage } from './VolumesListPage';
import { SessionProvider } from '../../hooks/use-session';
import { renderWithProviders } from '../../test-utils/TestProviders';

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

const renderPage = () =>
  renderWithProviders(
    <SessionProvider role="tenant-user" username="test-user" tenantId="test-tenant">
      <VolumesListPage />
    </SessionProvider>,
  );

describe('VolumesListPage', () => {
  beforeEach(() => {
    vi.mocked(useListResource).mockReturnValue(
      mockQueryResult<VolumesListResponse>({
        data: makeListResponse([makeVolume('v-1', 'web-vol')]),
        isLoading: false,
        error: null,
      }),
    );
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

  it('renders empty state when no volumes are returned', async () => {
    vi.mocked(useListResource).mockReturnValue(
      mockQueryResult<VolumesListResponse>({
        data: makeListResponse([]),
        isLoading: false,
        error: null,
      }),
    );

    renderPage();

    expect(await screen.findByText('No volumes found')).toBeInTheDocument();
  });

  it('renders the name search input', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByPlaceholderText('Search volumes by name…')).toBeInTheDocument();
    });
  });

  it('renders the state filter dropdown', async () => {
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

  it('renders the Create volume button linking to /storage/volumes/create', async () => {
    renderPage();

    const createButton = await screen.findByRole('link', { name: /Create volume/i });
    expect(createButton).toBeInTheDocument();
    expect(createButton).toHaveAttribute('href', '/storage/volumes/create');
  });

  it('hides the Create volume button on error', async () => {
    vi.mocked(useListResource).mockReturnValue(
      mockQueryResult<VolumesListResponse>({
        data: undefined,
        isLoading: false,
        error: new Error('Network error'),
      }),
    );

    renderPage();

    await screen.findByText('An error occurred');
    expect(screen.queryByRole('link', { name: /Create volume/i })).not.toBeInTheDocument();
  });

  it('does not pass a custom refetchInterval to useListResource', async () => {
    renderPage();

    await waitFor(() => {
      expect(vi.mocked(useListResource).mock.calls.length).toBeGreaterThan(0);
    });
    const options = vi.mocked(useListResource).mock.calls[0][2];
    expect(options).toBeUndefined();
  });
});
