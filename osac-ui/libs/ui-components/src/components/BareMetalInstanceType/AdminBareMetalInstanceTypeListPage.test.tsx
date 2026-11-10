import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  BareMetalInstanceTypeSchema,
  BareMetalInstanceTypesDeleteResponseSchema,
  type BareMetalInstanceType as PrivateBareMetalInstanceType,
} from '@osac/types/private';

import AdminBareMetalInstanceTypeListPage from './AdminBareMetalInstanceTypeListPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

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

const renderPage = (items: PrivateBareMetalInstanceType[] = []) =>
  renderWithProviders(<AdminBareMetalInstanceTypeListPage />, {
    apiFixtures: { privateBaremetalInstanceTypes: items },
  });

describe('AdminBareMetalInstanceTypeListPage', () => {
  it('renders the required columns and values for populated data', async () => {
    renderPage([
      makeBareMetalInstanceType('gpu-1', { acceleratorCount: 4 }),
      makeBareMetalInstanceType('cpu-1', { withHardware: false }),
    ]);

    await screen.findByText('bm-type-gpu-1');
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
    renderPage();

    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });

  it('shows the empty state when no bare metal instance types are returned', async () => {
    renderPage();

    expect(await screen.findByText('No bare metal instance types yet.')).toBeInTheDocument();
    expect(screen.getByRole('grid', { name: 'Bare metal instance types' })).toBeInTheDocument();
  });

  it('uses the page-level error state when the query fails', async () => {
    renderWithProviders(<AdminBareMetalInstanceTypeListPage />, {
      transportOverrides: {
        onBaremetalInstanceTypeList: () => {
          throw new ConnectError('Bare metal instance types unavailable', Code.Unavailable);
        },
      },
    });

    expect(await screen.findByText('An error occurred')).toBeInTheDocument();
    expect(screen.getByText('Bare metal instance types unavailable')).toBeInTheDocument();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it('sends the delete request and removes the row after the list re-fetches', async () => {
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
