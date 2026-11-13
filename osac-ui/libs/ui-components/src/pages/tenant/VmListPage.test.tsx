import { Code, ConnectError } from '@connectrpc/connect';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { ComputeInstance } from '@osac/types';
import { ComputeInstanceState } from '@osac/types';

import { VmListPage } from './VmListPage';
import type { MockTransportOverrides } from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

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

const renderPage = (transportOverrides?: MockTransportOverrides) =>
  renderWithProviders(<VmListPage />, {
    apiFixtures: { computeInstances: [vm] },
    session: { role: 'tenant-user' },
    transportOverrides,
  });

describe('VmListPage', () => {
  it('shows an alert and still renders the table when instance types fail to load', async () => {
    const { user } = renderPage({
      onPublicInstanceTypeList: () => {
        throw new ConnectError('Instance types unavailable', Code.Unavailable);
      },
    });

    expect(await screen.findByRole('link', { name: 'web-01' })).toBeInTheDocument();
    expect(screen.getByText('Standard 4 vCPU / 8 GiB')).toBeInTheDocument();

    await user.click(
      screen.getByRole('button', { name: 'Filter instance type by All instance types' }),
    );

    expect(await screen.findByText('Unable to load instance types')).toBeInTheDocument();
    expect(screen.getByText('Instance types unavailable')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Create virtual machine' })).toHaveAttribute(
      'href',
      '/vms/create',
    );
  });

  it('keeps compute instance failures on the page-level error path', async () => {
    renderPage({
      onComputeInstanceList: () => {
        throw new ConnectError('VMs unavailable', Code.Unavailable);
      },
    });

    expect(await screen.findByText('An error occurred')).toBeInTheDocument();
    expect(screen.getByText('VMs unavailable')).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'web-01' })).not.toBeInTheDocument();
  });
});
