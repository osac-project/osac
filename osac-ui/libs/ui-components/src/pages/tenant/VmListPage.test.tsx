import { screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { ComputeInstance } from '@osac/types';
import {
  ComputeInstanceState,
  ComputeInstances,
  ExternalIPAttachments,
  InstanceTypes,
} from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';
import { mockQueryResult } from '@osac/ui-components/test-utils/query';

import { VmListPage } from './VmListPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.mock('@osac/ui-components/api/use-resource', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@osac/ui-components/api/use-resource')>();
  return {
    ...actual,
    useListResource: vi.fn(),
  };
});

const vm = {
  id: 'vm-1',
  metadata: { name: 'web-01' },
  spec: {
    instanceType: { id: 'standard-4-8', name: 'Standard 4 vCPU / 8 GiB' },
  },
  status: {
    state: ComputeInstanceState.RUNNING,
    internalIpAddress: '10.0.0.5',
    externalIpAddress: '203.0.113.1',
  },
} as ComputeInstance;

const mockComputeListSuccess = () =>
  mockQueryResult({
    data: { items: [vm], total: 1 },
    isLoading: false,
    error: null,
  });

const mockComputeTotalsSuccess = () =>
  mockQueryResult({
    data: { items: [], total: 1 },
    isLoading: false,
    error: null,
  });

const mockEmptyListSuccess = () =>
  mockQueryResult({
    data: { items: [] },
    isLoading: false,
    error: null,
  });

const setupDefaultListMocks = () => {
  vi.mocked(useListResource).mockImplementation((service, request) => {
    if (service === ComputeInstances) {
      const limit = (request as { limit?: number })?.limit;
      return limit === 0 ? mockComputeTotalsSuccess() : mockComputeListSuccess();
    }
    if (service === InstanceTypes) {
      return mockEmptyListSuccess();
    }
    if (service === ExternalIPAttachments) {
      return mockEmptyListSuccess();
    }
    return mockEmptyListSuccess();
  });
};

const renderPage = () => renderWithProviders(<VmListPage />, { session: { role: 'tenant-user' } });

describe('VmListPage', () => {
  beforeEach(() => {
    setupDefaultListMocks();
  });

  it('shows an alert and still renders the table when instance types fail to load', async () => {
    vi.mocked(useListResource).mockImplementation((service, request) => {
      if (service === InstanceTypes) {
        return mockQueryResult({
          data: undefined,
          isLoading: false,
          error: new Error('Instance types unavailable'),
        });
      }
      if (service === ComputeInstances) {
        const limit = (request as { limit?: number })?.limit;
        return limit === 0 ? mockComputeTotalsSuccess() : mockComputeListSuccess();
      }
      return mockEmptyListSuccess();
    });

    const { user } = renderPage();

    expect(await screen.findByRole('link', { name: 'web-01' })).toBeInTheDocument();
    expect(screen.getByText('Standard 4 vCPU / 8 GiB')).toBeInTheDocument();

    await user.click(
      screen.getByRole('button', { name: 'Filter instance type by All instance types' }),
    );

    expect(screen.getByText('Unable to load instance types')).toBeInTheDocument();
    expect(screen.getByText('Instance types unavailable')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Create virtual machine' })).toHaveAttribute(
      'href',
      '/vms/create',
    );
  });

  it('keeps compute instance failures on the page-level error path', async () => {
    vi.mocked(useListResource).mockImplementation((service) => {
      if (service === ComputeInstances) {
        return mockQueryResult({
          data: { items: [] },
          isLoading: false,
          error: new Error('VMs unavailable'),
        });
      }
      return mockEmptyListSuccess();
    });

    renderPage();

    expect(await screen.findByText('An error occurred')).toBeInTheDocument();
    expect(screen.getByText('VMs unavailable')).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'web-01' })).not.toBeInTheDocument();
  });
});
