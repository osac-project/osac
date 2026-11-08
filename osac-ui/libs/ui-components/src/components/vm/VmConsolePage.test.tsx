import { Route, Routes } from 'react-router-dom';
import { screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { ComputeInstance } from '@osac/types';
import { ComputeInstanceState, ComputeInstances } from '@osac/types';

import { VmConsolePage } from './VmConsolePage';
import { renderWithProviders } from '../../test-utils/TestProviders';

const mocks = vi.hoisted(() => ({ useGetResource: vi.fn() }));

vi.mock('../../api/use-resource', () => ({
  useGetResource: mocks.useGetResource,
}));

vi.mock('./console/VmConsoleTab', () => ({
  default: ({ vm }: { vm: ComputeInstance }) => <div>Console for {vm.id}</div>,
}));

const vm = {
  id: 'vm-1',
  metadata: { name: 'web-01' },
  status: { state: ComputeInstanceState.RUNNING },
} as ComputeInstance;

const renderPage = () =>
  renderWithProviders(
    <Routes>
      <Route path="/vms/:id/console" element={<VmConsolePage />} />
    </Routes>,
    { routerEntries: ['/vms/vm-1/console'] },
  );

describe('VmConsolePage', () => {
  beforeEach(() => {
    mocks.useGetResource.mockReset();
  });

  it('loads the VM using the route id and renders the console', () => {
    mocks.useGetResource.mockReturnValue({
      data: { object: vm },
      error: null,
      isLoading: false,
      refetch: vi.fn(),
    });

    renderPage();

    expect(screen.getByText('Console for vm-1')).toBeInTheDocument();
    expect(screen.getByText('Console for vm-1').closest('.vm-console-page')).toBeInTheDocument();
    expect(mocks.useGetResource).toHaveBeenCalledWith(
      ComputeInstances,
      { id: 'vm-1' },
      { enabled: true },
    );
  });

  it('shows the standard loading state', () => {
    mocks.useGetResource.mockReturnValue({
      data: undefined,
      error: null,
      isLoading: true,
      refetch: vi.fn(),
    });

    renderPage();

    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });

  it('shows the standard error state', () => {
    mocks.useGetResource.mockReturnValue({
      data: undefined,
      error: new Error('request failed'),
      isLoading: false,
      refetch: vi.fn(),
    });

    renderPage();

    expect(
      screen.getByRole('heading', { name: 'Failed to fetch capabilities' }),
    ).toBeInTheDocument();
  });

  it('shows the standard not-found state', () => {
    mocks.useGetResource.mockReturnValue({
      data: undefined,
      error: null,
      isLoading: false,
      refetch: vi.fn(),
    });

    renderPage();

    expect(screen.getByRole('heading', { name: 'Virtual machine not found' })).toBeInTheDocument();
  });
});
