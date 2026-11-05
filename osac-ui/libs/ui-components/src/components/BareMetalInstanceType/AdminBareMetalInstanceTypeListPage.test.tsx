import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  BareMetalInstanceTypeSchema,
  BareMetalInstanceTypes,
  BareMetalInstanceTypesDeleteResponseSchema,
  type BareMetalInstanceTypesListResponse,
  BareMetalInstanceTypesListResponseSchema,
  type BareMetalInstanceType as PrivateBareMetalInstanceType,
} from '@osac/types/private';
import { useListResource } from '@osac/ui-components/api/use-resource';
import { getViewTypePrefKey } from '@osac/ui-components/hooks/use-view-type';
import { mockQueryResult } from '@osac/ui-components/test-utils/query';

import AdminBareMetalInstanceTypeListPage from './AdminBareMetalInstanceTypeListPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.mock('@osac/ui-components/api/use-resource', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@osac/ui-components/api/use-resource')>();
  return { ...actual, useListResource: vi.fn() };
});

const { useListResource: useListResourceActual } = await vi.importActual<
  typeof import('@osac/ui-components/api/use-resource')
>('@osac/ui-components/api/use-resource');

const VIEW_PREF_KEY = getViewTypePrefKey('admin-bare-metal-instance-types-list');

const makeBareMetalInstanceType = (
  id: string,
  overrides?: {
    acceleratorCount?: number;
    withHardware?: boolean;
  },
): PrivateBareMetalInstanceType =>
  create(BareMetalInstanceTypeSchema, {
    id,
    metadata: { name: `bm-type-${id}` },
    spec: {
      hardware:
        overrides?.withHardware === false
          ? undefined
          : {
              cpu: { cores: 64, architecture: 'x86_64' },
              memory: { totalGb: 256n },
              accelerators: Array.from({ length: overrides?.acceleratorCount ?? 0 }, () => ({
                type: 'GPU',
                model: 'A100',
              })),
            },
    },
  });

const makeListResponse = (
  items: PrivateBareMetalInstanceType[],
  total = items.length,
): BareMetalInstanceTypesListResponse =>
  create(BareMetalInstanceTypesListResponseSchema, {
    items,
    size: items.length,
    total,
  });

const mockListSuccess = (items: PrivateBareMetalInstanceType[], total?: number) =>
  mockQueryResult<BareMetalInstanceTypesListResponse>({
    data: makeListResponse(items, total),
    isLoading: false,
    error: null,
  });

const mockTotalsSuccess = (total: number) =>
  mockQueryResult<BareMetalInstanceTypesListResponse>({
    data: makeListResponse([], total),
    isLoading: false,
    error: null,
  });

const setupDefaultListMocks = (items: PrivateBareMetalInstanceType[], total?: number) => {
  vi.mocked(useListResource).mockImplementation((_service, request) => {
    const limit = (request as { limit?: number })?.limit;
    return limit === 0 ? mockTotalsSuccess(total ?? items.length) : mockListSuccess(items, total);
  });
};

const renderPage = () => renderWithProviders(<AdminBareMetalInstanceTypeListPage />);

describe('AdminBareMetalInstanceTypeListPage', () => {
  beforeEach(() => {
    localStorage.setItem(VIEW_PREF_KEY, 'list');
  });

  afterEach(() => {
    vi.mocked(useListResource).mockReset();
    localStorage.removeItem(VIEW_PREF_KEY);
  });

  it('renders the required columns and values for populated data', async () => {
    setupDefaultListMocks([
      makeBareMetalInstanceType('gpu-1', { acceleratorCount: 4 }),
      makeBareMetalInstanceType('cpu-1', { withHardware: false }),
    ]);

    renderPage();

    await screen.findByText('bm-type-gpu-1');
    expect(screen.getByRole('heading', { name: 'Bare metal instance types' })).toBeInTheDocument();
    expect(screen.getAllByRole('columnheader').map((header) => header.textContent)).toEqual([
      'Name',
      'CPU',
      'Memory (GB)',
      'Accelerators',
      '',
    ]);
    expect(screen.getByText('bm-type-gpu-1')).toBeInTheDocument();
    expect(screen.getByText('64 (x86_64)')).toBeInTheDocument();
    expect(screen.getByText('256')).toBeInTheDocument();
    expect(screen.getByText('4')).toBeInTheDocument();
    // The hardware-less row renders em dashes for CPU, memory and accelerators.
    expect(screen.getAllByText('—').length).toBeGreaterThanOrEqual(3);
  });

  it('shows a loading spinner while the query is in flight', () => {
    vi.mocked(useListResource).mockReturnValue(
      mockQueryResult<BareMetalInstanceTypesListResponse>({
        data: undefined,
        isLoading: true,
        error: null,
      }),
    );

    renderPage();

    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });

  it('shows the empty state when no bare metal instance types are returned', () => {
    setupDefaultListMocks([]);

    renderPage();

    expect(screen.getByText('No bare metal instance types found')).toBeInTheDocument();
    expect(screen.getByText('No bare metal instance types are available yet.')).toBeInTheDocument();
    expect(
      screen.queryByRole('grid', { name: 'Bare metal instance types' }),
    ).not.toBeInTheDocument();
  });

  it('uses the page-level error state when the query fails', () => {
    vi.mocked(useListResource).mockImplementation((service) => {
      if (service === BareMetalInstanceTypes) {
        return mockQueryResult<BareMetalInstanceTypesListResponse>({
          data: makeListResponse([]),
          isLoading: false,
          error: new Error('Bare metal instance types unavailable'),
        });
      }
      return mockQueryResult<BareMetalInstanceTypesListResponse>({
        data: makeListResponse([]),
        isLoading: false,
        error: null,
      });
    });

    renderPage();

    expect(screen.getByText('An error occurred')).toBeInTheDocument();
    expect(screen.getByText('Bare metal instance types unavailable')).toBeInTheDocument();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it('sends the delete request and removes the row after the list re-fetches', async () => {
    vi.mocked(useListResource).mockImplementation(useListResourceActual);
    const items = [makeBareMetalInstanceType('gpu-1')];
    let deleteCalled = false;

    const { user } = renderWithProviders(<AdminBareMetalInstanceTypeListPage />, {
      apiFixtures: { privateBaremetalInstanceTypes: items },
      transportOverrides: {
        onBaremetalInstanceTypeDelete: (req) => {
          deleteCalled = true;
          const index = items.findIndex((item) => item.id === req.id);
          if (index >= 0) {
            items.splice(index, 1);
          }
          return create(BareMetalInstanceTypesDeleteResponseSchema);
        },
      },
    });

    await screen.findByText('bm-type-gpu-1');

    await user.click(screen.getByRole('button', { name: 'Actions for bm-type-gpu-1' }));
    await user.click(screen.getByRole('menuitem', { name: 'Delete' }));
    await user.click(screen.getByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(screen.queryByText('bm-type-gpu-1')).not.toBeInTheDocument());
    expect(deleteCalled).toBe(true);
    expect(screen.getByText('No bare metal instance types found')).toBeInTheDocument();
  });

  it('shows an inline error alert when the delete request fails', async () => {
    vi.mocked(useListResource).mockImplementation(useListResourceActual);
    const items = [makeBareMetalInstanceType('gpu-1')];

    const { user } = renderWithProviders(<AdminBareMetalInstanceTypeListPage />, {
      apiFixtures: { privateBaremetalInstanceTypes: items },
      transportOverrides: {
        onBaremetalInstanceTypeDelete: () => {
          throw new ConnectError('Instance type is still in use', Code.FailedPrecondition);
        },
      },
    });

    await screen.findByText('bm-type-gpu-1');

    await user.click(screen.getByRole('button', { name: 'Actions for bm-type-gpu-1' }));
    await user.click(screen.getByRole('menuitem', { name: 'Delete' }));
    await user.click(screen.getByRole('button', { name: 'Delete' }));

    await screen.findByText('Failed to delete bare metal instance type');
    expect(screen.getByText('Instance type is still in use')).toBeInTheDocument();
    expect(screen.getByText('bm-type-gpu-1')).toBeInTheDocument();
  });
});
