import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  InstanceTypeSchema,
  InstanceTypeState,
  InstanceTypesDeleteResponseSchema,
  type InstanceTypesListResponse,
  InstanceTypesListResponseSchema,
  InstanceTypesUpdateResponseSchema,
  type InstanceType as PrivateInstanceType,
} from '@osac/types/private';
import { mockQueryResult } from '@osac/ui-components/test-utils/query';

import AdminInstanceTypeListPage from './AdminInstanceTypeListPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.mock('@osac/ui-components/api/use-resource', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@osac/ui-components/api/use-resource')>();
  return { ...actual, useListResource: vi.fn() };
});

const { useListResource } = await import('@osac/ui-components/api/use-resource');
const { useListResource: useListResourceActual } = await vi.importActual<
  typeof import('@osac/ui-components/api/use-resource')
>('@osac/ui-components/api/use-resource');

const makeInstanceType = (
  id: string,
  state: InstanceTypeState,
  gpu?: { pciDeviceSelector: string; resourceName: string; count: number },
): PrivateInstanceType =>
  create(InstanceTypeSchema, {
    id,
    metadata: {
      name: `instance-type-${id}`,
      creationTimestamp: { seconds: BigInt(1717000000), nanos: 0 },
    },
    spec: {
      vcpus: 4,
      memoryGib: 16,
      description: `${id} description`,
      state,
      gpu,
    },
  });

const makeListResponse = (items: PrivateInstanceType[]): InstanceTypesListResponse =>
  create(InstanceTypesListResponseSchema, {
    items,
    size: items.length,
    total: items.length,
  });

const renderPage = () => renderWithProviders(<AdminInstanceTypeListPage />);

const renderPageWithCreateRoute = () =>
  renderWithProviders(
    <Routes>
      <Route path="/admin/infrastructure/instance-types" element={<AdminInstanceTypeListPage />} />
      <Route
        path="/admin/infrastructure/instance-types/create"
        element={<h1>Create instance type page</h1>}
      />
    </Routes>,
    { routerEntries: ['/admin/infrastructure/instance-types'] },
  );

describe('AdminInstanceTypeListPage', () => {
  afterEach(() => {
    vi.mocked(useListResource).mockReset();
  });

  it('renders the required columns and lifecycle labels for populated data', () => {
    vi.mocked(useListResource).mockReturnValue(
      mockQueryResult<InstanceTypesListResponse>({
        data: makeListResponse([
          makeInstanceType('active-1', InstanceTypeState.ACTIVE, {
            pciDeviceSelector: '10DE:20B0',
            resourceName: 'nvidia.com/A100',
            count: 2,
          }),
          makeInstanceType('deprecated-1', InstanceTypeState.DEPRECATED),
          makeInstanceType('obsolete-1', InstanceTypeState.OBSOLETE),
        ]),
      }),
    );

    renderPage();

    expect(screen.getByRole('heading', { name: 'Instance types' })).toBeInTheDocument();
    expect(screen.getByText('Infrastructure').closest('.pf-v6-c-label')).not.toBeNull();
    expect(screen.getAllByRole('columnheader').map((header) => header.textContent)).toEqual([
      'Name',
      'Lifecycle state',
      'vCPUs',
      'Memory (GiB)',
      'GPUs',
      'Created',
      '',
    ]);
    expect(screen.getByRole('link', { name: 'instance-type-active-1' })).toBeInTheDocument();
    expect(screen.getByText('2')).toBeInTheDocument();
    expect(screen.getAllByText('—').length).toBeGreaterThanOrEqual(2);
    expect(screen.getByText('Active')).toBeInTheDocument();
    expect(screen.getByText('Deprecated')).toBeInTheDocument();
    expect(screen.getByText('Obsolete')).toBeInTheDocument();
  });

  it('shows a loading spinner while the query is in flight', () => {
    vi.mocked(useListResource).mockReturnValue(
      mockQueryResult<InstanceTypesListResponse>({
        data: undefined,
        isLoading: true,
      }),
    );

    renderPage();

    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });

  it('shows the empty state when no instance types are returned', () => {
    vi.mocked(useListResource).mockReturnValue(
      mockQueryResult<InstanceTypesListResponse>({
        data: makeListResponse([]),
      }),
    );

    renderPage();

    expect(screen.getByText('No instance types yet.')).toBeInTheDocument();
    expect(
      screen.getByText('Create an instance type to start defining provider-managed sizes.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('grid', { name: 'Instance types' })).toBeInTheDocument();
  });

  it('uses the page-level error state when the query fails', () => {
    vi.mocked(useListResource).mockReturnValue(
      mockQueryResult<InstanceTypesListResponse>({
        data: makeListResponse([]),
        error: new Error('Private instance types unavailable'),
      }),
    );

    renderPage();

    expect(screen.getByText('An error occurred')).toBeInTheDocument();
    expect(screen.getByText('Private instance types unavailable')).toBeInTheDocument();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it('navigates to the create route when the create button is clicked', async () => {
    vi.mocked(useListResource).mockReturnValue(
      mockQueryResult<InstanceTypesListResponse>({
        data: makeListResponse([]),
      }),
    );

    const { user } = renderPageWithCreateRoute();

    await user.click(screen.getByRole('link', { name: 'Create instance type' }));

    expect(screen.getByRole('heading', { name: 'Create instance type page' })).toBeInTheDocument();
  });

  it('sends the deprecate request and re-fetches the list to reflect the new state', async () => {
    vi.mocked(useListResource).mockImplementation(useListResourceActual);
    const items = [makeInstanceType('active-1', InstanceTypeState.ACTIVE)];
    let captured: Record<string, unknown> | undefined;
    let listCalls = 0;

    const { user } = renderWithProviders(<AdminInstanceTypeListPage />, {
      apiFixtures: { privateInstanceTypes: items },
      transportOverrides: {
        onInstanceTypeList: () => {
          listCalls += 1;
          return makeListResponse(items);
        },
        onInstanceTypeUpdate: (req) => {
          captured = req as unknown as Record<string, unknown>;
          items[0] = makeInstanceType('active-1', InstanceTypeState.DEPRECATED);
          return create(InstanceTypesUpdateResponseSchema, { object: items[0] });
        },
      },
    });

    await screen.findByText('instance-type-active-1');
    const listCallsBeforeAction = listCalls;

    await user.click(screen.getByRole('button', { name: 'Actions for instance-type-active-1' }));
    await user.click(screen.getByRole('menuitem', { name: 'Deprecate' }));

    await waitFor(() => expect(screen.getByText('Deprecated')).toBeInTheDocument());
    const object = captured?.object as { id?: string; spec?: { state?: InstanceTypeState } };
    expect(object.id).toBe('active-1');
    expect(object.spec?.state).toBe(InstanceTypeState.DEPRECATED);
    expect(listCalls).toBeGreaterThan(listCallsBeforeAction);
  });

  it('sends the delete request and removes the row after the list re-fetches', async () => {
    vi.mocked(useListResource).mockImplementation(useListResourceActual);
    const items = [makeInstanceType('obsolete-1', InstanceTypeState.OBSOLETE)];
    let deleteCalled = false;

    const { user } = renderWithProviders(<AdminInstanceTypeListPage />, {
      apiFixtures: { privateInstanceTypes: items },
      transportOverrides: {
        onInstanceTypeDelete: (req) => {
          deleteCalled = true;
          const index = items.findIndex((item) => item.id === req.id);
          if (index >= 0) {
            items.splice(index, 1);
          }
          return create(InstanceTypesDeleteResponseSchema);
        },
      },
    });

    await screen.findByText('instance-type-obsolete-1');

    await user.click(screen.getByRole('button', { name: 'Actions for instance-type-obsolete-1' }));
    await user.click(screen.getByRole('menuitem', { name: 'Delete' }));
    await user.click(screen.getByRole('button', { name: 'Delete' }));

    await waitFor(() =>
      expect(screen.queryByText('instance-type-obsolete-1')).not.toBeInTheDocument(),
    );
    expect(deleteCalled).toBe(true);
    expect(screen.getByText('No instance types yet.')).toBeInTheDocument();
  });
});
