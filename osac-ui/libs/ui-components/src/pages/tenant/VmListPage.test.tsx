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

const renderPage = () =>
  renderWithProviders(
    <SessionProvider role="tenant-user" username="test-user" tenantId="test-tenant">
      <VmListPage />
    </SessionProvider>,
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

  it('shows an alert and still renders the table when instance types fail to load', () => {
    vi.mocked(useInstanceTypes).mockReturnValue(
      mockQueryResult({
        data: [],
        isLoading: false,
        error: new Error('Instance types unavailable'),
      }),
    );

    renderPage();

    expect(screen.getByText('Could not load instance types')).toBeInTheDocument();
    expect(screen.getByText('Instance types unavailable')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Create virtual machine' })).toHaveAttribute(
      'href',
      '/vms/create',
    );
    expect(screen.getByRole('link', { name: 'web-01' })).toBeInTheDocument();
    expect(screen.getByText('standard-4-8')).toBeInTheDocument();
  });

  it('keeps compute instance failures on the page-level error path', () => {
    vi.mocked(useComputeInstances).mockReturnValue(
      mockQueryResult({
        data: [],
        isLoading: false,
        error: new Error('VMs unavailable'),
      }),
    );

    renderPage();

    expect(screen.getByText('An error occurred')).toBeInTheDocument();
    expect(screen.getByText('VMs unavailable')).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'web-01' })).not.toBeInTheDocument();
  });
});
