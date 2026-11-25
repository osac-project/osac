import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { type Subnet, SubnetState, type VirtualNetwork, VirtualNetworkState } from '@osac/types';

import { SubnetsTable } from './SubnetsTable';
import { renderWithProviders } from '../../test-utils/TestProviders';

const networks = new Map<string, VirtualNetwork>([
  [
    'vn-1',
    {
      id: 'vn-1',
      metadata: { name: 'app-net' },
      status: { state: VirtualNetworkState.READY },
    } as VirtualNetwork,
  ],
]);

const makeSubnet = (overrides: Record<string, unknown> = {}) =>
  ({
    id: 'subnet-1',
    metadata: { name: 'web-a', project: 'web-team' },
    spec: { ipv4Cidr: '10.20.1.0/24', virtualNetwork: { id: 'vn-1' } },
    status: { state: SubnetState.READY },
    ...overrides,
  }) as Subnet;

const renderTable = (subnets: Subnet[], options: { showVirtualNetwork?: boolean } = {}) =>
  renderWithProviders(
    <SubnetsTable
      subnets={subnets}
      virtualNetworksById={networks}
      showVirtualNetwork={options.showVirtualNetwork}
    />,
  );

describe('SubnetsTable', () => {
  it('renders Name, Status, IPv4 CIDR, Virtual network and Project', () => {
    renderTable([makeSubnet()]);

    const table = screen.getByRole('grid', { name: 'Subnets' });
    expect(within(table).getByText('Name')).toBeInTheDocument();
    expect(within(table).getByText('Status')).toBeInTheDocument();
    expect(within(table).getByText('IPv4 CIDR')).toBeInTheDocument();
    expect(within(table).getByText('Virtual network')).toBeInTheDocument();
    expect(within(table).getByText('Project')).toBeInTheDocument();
    expect(within(table).getByText('web-a')).toBeInTheDocument();
    expect(within(table).getByText('10.20.1.0/24')).toBeInTheDocument();
    expect(within(table).getByText('web-team')).toBeInTheDocument();
  });

  it('links the parent network and does not link the subnet name', () => {
    renderTable([makeSubnet()]);

    expect(screen.getByRole('link', { name: 'app-net' })).toHaveAttribute(
      'href',
      '/networking/virtual-networks/vn-1',
    );
    expect(screen.queryByRole('link', { name: 'web-a' })).not.toBeInTheDocument();
  });

  it('shows Root for a resource in the tenant default project', () => {
    renderTable([makeSubnet({ metadata: { name: 'default', project: '' } })]);

    expect(screen.getByText('Root')).toBeInTheDocument();
  });

  it('marks Default and Auto subnets with a badge', () => {
    renderTable([
      makeSubnet({
        id: 'subnet-default',
        metadata: { name: 'default', labels: { 'osac.openshift.io/default': 'true' } },
      }),
      makeSubnet({
        id: 'subnet-auto',
        metadata: { name: 'auto-subnet', labels: { 'osac.openshift.io/auto-created-for': 'vm-1' } },
      }),
    ]);

    expect(screen.getByText('Default')).toBeInTheDocument();
    expect(screen.getByText('Auto')).toBeInTheDocument();
  });

  it('can hide the parent column inside a network', () => {
    renderTable([makeSubnet()], { showVirtualNetwork: false });

    expect(screen.queryByText('Virtual network')).not.toBeInTheDocument();
  });

  it('renders the row actions from the caller', () => {
    renderWithProviders(
      <SubnetsTable
        subnets={[makeSubnet()]}
        virtualNetworksById={networks}
        renderActions={() => <button type="button">Row action</button>}
      />,
    );

    expect(screen.getByRole('button', { name: 'Row action' })).toBeInTheDocument();
  });
});
