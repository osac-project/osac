import { screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { ComputeInstance } from '@osac/types';
import { ComputeInstanceState } from '@osac/types';
import { mockQueryResult } from '@osac/ui-components/test-utils/query';

import { VmListPage } from './VmListPage';
import { SessionProvider } from '../../hooks/use-session';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.mock('@osac/ui-components/api/v1/compute-instance', () => ({
  useComputeInstances: vi.fn(),
}));

vi.mock('@osac/ui-components/api/v1/instance-types', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@osac/ui-components/api/v1/instance-types')>();
  return {
    ...actual,
    useInstanceTypes: vi.fn(),
  };
});

vi.mock('../../components/vm/VmActionsMenu', () => ({
  VmActionsMenu: () => <button type="button">Actions</button>,
}));

vi.mock('../../components/vm/VmInstanceTypeLabel', () => ({
  VmInstanceTypeLabel: ({
    instanceTypeId,
    instanceType,
  }: {
    instanceTypeId?: string;
    instanceType?: { metadata?: { name?: string } };
  }) => <span>{instanceType?.metadata?.name ?? instanceTypeId ?? '—'}</span>,
}));

const { useComputeInstances } = await import('@osac/ui-components/api/v1/compute-instance');
const { useInstanceTypes } = await import('@osac/ui-components/api/v1/instance-types');

const vm = {
  id: 'vm-1',
  metadata: { name: 'web-01' },
  spec: { instanceType: { id: 'standard-4-8' } },
  status: {
    state: ComputeInstanceState.RUNNING,
    internalIpAddress: '10.0.0.5',
    externalIpAddress: '203.0.113.1',
  },
} as ComputeInstance;

const renderPage = (routerEntries?: string[]) =>
  renderWithProviders(
    <SessionProvider role="tenant-user" username="test-user" tenantId="test-tenant">
      <VmListPage />
    </SessionProvider>,
    { routerEntries },
  );

describe('VmListPage', () => {
  beforeEach(() => {
    vi.mocked(useComputeInstances).mockReturnValue({
      data: [vm],
      isLoading: false,
      error: null,
    } as ReturnType<typeof useComputeInstances>);

    vi.mocked(useInstanceTypes).mockReturnValue({
      data: [{ id: 'standard-4-8', metadata: { name: 'Standard 4 vCPU / 8 GiB' } }],
      isLoading: false,
      error: null,
    } as ReturnType<typeof useInstanceTypes>);
  });

  it('shows an alert and still renders the table when instance types fail to load', async () => {
    vi.mocked(useInstanceTypes).mockReturnValue(
      mockQueryResult({
        data: [],
        isLoading: false,
        error: new Error('Instance types unavailable'),
      }),
    );

    renderPage();

    expect(await screen.findByText('Could not load instance types')).toBeInTheDocument();
    expect(screen.getByText('Instance types unavailable')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Create virtual machine' })).toHaveAttribute(
      'href',
      '/vms/create',
    );
    expect(screen.getByRole('link', { name: 'web-01' })).toBeInTheDocument();
    expect(screen.getByText('standard-4-8')).toBeInTheDocument();
  });

  it('keeps compute instance failures on the page-level error path', async () => {
    vi.mocked(useComputeInstances).mockReturnValue(
      mockQueryResult({
        data: [],
        isLoading: false,
        error: new Error('VMs unavailable'),
      }),
    );

    renderPage();

    expect(await screen.findByText('An error occurred')).toBeInTheDocument();
    expect(screen.getByText('VMs unavailable')).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'web-01' })).not.toBeInTheDocument();
  });

  it('shows the search term when no virtual machines match the name search', async () => {
    const { user } = renderPage();

    await user.type(await screen.findByLabelText('Filter virtual machines by name'), 'test-polina');

    expect(
      screen.getByText('No virtual machines found matching "test-polina".'),
    ).toBeInTheDocument();
    expect(
      screen.queryByText('Choose one or more statuses above to filter virtual machines.'),
    ).not.toBeInTheDocument();
  });

  it('shows the search term when a status filter also leaves no matches', async () => {
    renderPage(['/?status=running&search=test-polina']);

    expect(
      await screen.findByText('No virtual machines found matching "test-polina".'),
    ).toBeInTheDocument();
    expect(screen.queryByText('No virtual machines match your filters.')).not.toBeInTheDocument();
  });

  it('shows the filter message when a matching name has a different status', async () => {
    renderPage(['/?status=stopped&search=web-01']);

    expect(await screen.findByText('No virtual machines match your filters.')).toBeInTheDocument();
    expect(
      screen.queryByText('No virtual machines found matching "web-01".'),
    ).not.toBeInTheDocument();
  });

  it('retains the status-only empty message', async () => {
    renderPage(['/?status=stopped']);

    expect(await screen.findByText('No virtual machines match your filters.')).toBeInTheDocument();
  });

  it('retains the no-VM message with a status filter', async () => {
    vi.mocked(useComputeInstances).mockReturnValue(
      mockQueryResult({ data: [], isLoading: false, error: null }),
    );

    renderPage(['/?status=running']);

    expect(
      await screen.findByText('No virtual machines yet. Create one to get started.'),
    ).toBeInTheDocument();
  });
});
