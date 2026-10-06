import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  InstanceTypeSchema,
  InstanceTypeState,
  InstanceTypesDeleteResponseSchema,
  type InstanceTypesGetResponse,
  InstanceTypesGetResponseSchema,
  type InstanceType as PrivateInstanceType,
} from '@osac/types/private';
import { mockQueryResult } from '@osac/ui-components/test-utils/query';

import AdminInstanceTypeDetailPage from './AdminInstanceTypeDetailPage';
import type { MockTransportOverrides } from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

vi.mock('@osac/ui-components/api/use-resource', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@osac/ui-components/api/use-resource')>();
  return { ...actual, useGetResource: vi.fn() };
});

const { useGetResource } = await import('@osac/ui-components/api/use-resource');

const LIST_ROUTE = '/admin/infrastructure/instance-types';
const DETAIL_ROUTE = `${LIST_ROUTE}/gp-small`;

const makeInstanceType = (
  state: InstanceTypeState = InstanceTypeState.ACTIVE,
  gpu?: { pciDeviceSelector: string; resourceName: string; count: number },
): PrivateInstanceType =>
  create(InstanceTypeSchema, {
    id: 'gp-small',
    metadata: {
      name: 'gp-small',
      creationTimestamp: { seconds: BigInt(1717000000), nanos: 0 },
    },
    spec: {
      vcpus: 4,
      memoryGib: 16,
      description: 'General purpose instance type',
      state,
      gpu,
    },
  });

const renderDetailPage = (
  instanceType: PrivateInstanceType,
  transportOverrides?: MockTransportOverrides,
) => {
  vi.mocked(useGetResource).mockReturnValue(
    mockQueryResult<InstanceTypesGetResponse>({
      data: create(InstanceTypesGetResponseSchema, { object: instanceType }),
    }),
  );
  return renderWithProviders(
    <Routes>
      <Route path={`${LIST_ROUTE}/:id`} element={<AdminInstanceTypeDetailPage />} />
    </Routes>,
    { routerEntries: [DETAIL_ROUTE], transportOverrides },
  );
};

describe('AdminInstanceTypeDetailPage', () => {
  afterEach(() => {
    mockNavigate.mockReset();
    vi.mocked(useGetResource).mockReset();
  });

  it('renders the breadcrumb, title, description, and details', () => {
    renderDetailPage(makeInstanceType());

    expect(screen.getByRole('heading', { name: 'gp-small' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Instance types' })).toBeInTheDocument();
    expect(screen.getByText('General purpose instance type')).toBeInTheDocument();
    expect(screen.getByText('4')).toBeInTheDocument();
    expect(screen.getByText('16')).toBeInTheDocument();
    expect(screen.getByText('Active')).toBeInTheDocument();
    expect(screen.getByText('This instance type has no GPU attached.')).toBeInTheDocument();
  });

  it('renders GPU details when the instance type has a GPU', () => {
    renderDetailPage(
      makeInstanceType(InstanceTypeState.ACTIVE, {
        pciDeviceSelector: '10DE:20B0',
        resourceName: 'nvidia.com/A100',
        count: 2,
      }),
    );

    expect(screen.getByText('10DE:20B0')).toBeInTheDocument();
    expect(screen.getByText('nvidia.com/A100')).toBeInTheDocument();
    expect(screen.getByText('2')).toBeInTheDocument();
  });

  it('navigates back to the list when the breadcrumb link is clicked', async () => {
    const { user } = renderDetailPage(makeInstanceType());

    await user.click(screen.getByRole('button', { name: 'Instance types' }));

    expect(mockNavigate).toHaveBeenCalledWith(LIST_ROUTE);
  });

  it('shows a loading spinner while the query is in flight', () => {
    vi.mocked(useGetResource).mockReturnValue(
      mockQueryResult<InstanceTypesGetResponse>({ data: undefined, isLoading: true }),
    );

    renderWithProviders(
      <Routes>
        <Route path={`${LIST_ROUTE}/:id`} element={<AdminInstanceTypeDetailPage />} />
      </Routes>,
      { routerEntries: [DETAIL_ROUTE] },
    );

    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });

  it('uses the page-level error state when the query fails', () => {
    vi.mocked(useGetResource).mockReturnValue(
      mockQueryResult<InstanceTypesGetResponse>({
        data: undefined,
        error: new Error('Instance type unavailable'),
      }),
    );

    renderWithProviders(
      <Routes>
        <Route path={`${LIST_ROUTE}/:id`} element={<AdminInstanceTypeDetailPage />} />
      </Routes>,
      { routerEntries: [DETAIL_ROUTE] },
    );

    expect(screen.getByText('An error occurred')).toBeInTheDocument();
    expect(screen.getByText('Instance type unavailable')).toBeInTheDocument();
  });

  it('shows Deprecate and Mark obsolete buttons (not a kebab menu) for an active instance type', () => {
    renderDetailPage(makeInstanceType(InstanceTypeState.ACTIVE));

    expect(screen.getByRole('button', { name: 'Deprecate' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Obsolete' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Reactivate' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Actions for/ })).not.toBeInTheDocument();
  });

  it('shows Deprecate, Reactivate, and Delete buttons for an obsolete instance type', () => {
    renderDetailPage(makeInstanceType(InstanceTypeState.OBSOLETE));

    expect(screen.getByRole('button', { name: 'Deprecate' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Reactivate' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Obsolete' })).not.toBeInTheDocument();
  });

  it('deletes the instance type and navigates back to the list', async () => {
    let deleteCalled = false;
    const { user } = renderDetailPage(makeInstanceType(InstanceTypeState.OBSOLETE), {
      onInstanceTypeDelete: (req) => {
        deleteCalled = true;
        expect(req.id).toBe('gp-small');
        return create(InstanceTypesDeleteResponseSchema);
      },
    });

    await user.click(screen.getByRole('button', { name: 'Delete' }));
    const dialog = screen.getByRole('dialog');
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(mockNavigate).toHaveBeenCalledWith(LIST_ROUTE));
    expect(deleteCalled).toBe(true);
  });
});
