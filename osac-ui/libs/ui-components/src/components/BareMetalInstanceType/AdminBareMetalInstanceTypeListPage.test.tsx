import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  BareMetalInstanceTypeSchema,
  BareMetalInstanceTypesDeleteResponseSchema,
  type BareMetalInstanceType as PrivateBareMetalInstanceType,
} from '@osac/types/private';
import { mockQueryResult } from '@osac/ui-components/test-utils/query';

import AdminBareMetalInstanceTypeListPage from './AdminBareMetalInstanceTypeListPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.mock('@osac/ui-components/api/v1/private/baremetal-instance-type', async (importOriginal) => {
  const actual =
    await importOriginal<
      typeof import('@osac/ui-components/api/v1/private/baremetal-instance-type')
    >();
  return { ...actual, useAdminBareMetalInstanceTypes: vi.fn() };
});

const { useAdminBareMetalInstanceTypes } =
  await import('@osac/ui-components/api/v1/private/baremetal-instance-type');
const { useAdminBareMetalInstanceTypes: useAdminBareMetalInstanceTypesActual } =
  await vi.importActual<
    typeof import('@osac/ui-components/api/v1/private/baremetal-instance-type')
  >('@osac/ui-components/api/v1/private/baremetal-instance-type');

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

const renderPage = () => renderWithProviders(<AdminBareMetalInstanceTypeListPage />);

describe('AdminBareMetalInstanceTypeListPage', () => {
  afterEach(() => {
    vi.mocked(useAdminBareMetalInstanceTypes).mockReset();
  });

  it('renders the required columns and values for populated data', () => {
    vi.mocked(useAdminBareMetalInstanceTypes).mockReturnValue(
      mockQueryResult<PrivateBareMetalInstanceType[]>({
        data: [
          makeBareMetalInstanceType('gpu-1', { acceleratorCount: 4 }),
          makeBareMetalInstanceType('cpu-1', { withHardware: false }),
        ],
      }),
    );

    renderPage();

    expect(screen.getByRole('heading', { name: 'Bare metal instance types' })).toBeInTheDocument();
    expect(screen.getAllByRole('columnheader').map((header) => header.textContent)).toEqual([
      'Name',
      'CPU',
      'Memory (GiB)',
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
    vi.mocked(useAdminBareMetalInstanceTypes).mockReturnValue(
      mockQueryResult<PrivateBareMetalInstanceType[]>({
        data: undefined,
        isLoading: true,
      }),
    );

    renderPage();

    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });

  it('shows the empty state when no bare metal instance types are returned', () => {
    vi.mocked(useAdminBareMetalInstanceTypes).mockReturnValue(
      mockQueryResult<PrivateBareMetalInstanceType[]>({ data: [] }),
    );

    renderPage();

    expect(screen.getByText('No bare metal instance types yet.')).toBeInTheDocument();
    expect(screen.getByRole('grid', { name: 'Bare metal instance types' })).toBeInTheDocument();
  });

  it('uses the page-level error state when the query fails', () => {
    vi.mocked(useAdminBareMetalInstanceTypes).mockReturnValue(
      mockQueryResult<PrivateBareMetalInstanceType[]>({
        data: [],
        error: new Error('Bare metal instance types unavailable'),
      }),
    );

    renderPage();

    expect(screen.getByText('An error occurred')).toBeInTheDocument();
    expect(screen.getByText('Bare metal instance types unavailable')).toBeInTheDocument();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it('sends the delete request and removes the row after the list re-fetches', async () => {
    vi.mocked(useAdminBareMetalInstanceTypes).mockImplementation(
      useAdminBareMetalInstanceTypesActual,
    );
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
    expect(screen.getByText('No bare metal instance types yet.')).toBeInTheDocument();
  });

  it('shows an inline error alert when the delete request fails', async () => {
    vi.mocked(useAdminBareMetalInstanceTypes).mockImplementation(
      useAdminBareMetalInstanceTypesActual,
    );
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
