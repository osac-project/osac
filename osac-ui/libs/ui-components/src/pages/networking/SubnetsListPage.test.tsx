import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { Cluster, ComputeInstance, Project, Subnet, VirtualNetwork } from '@osac/types';
import { ProjectState, ServiceTier, SubnetState, VirtualNetworkState } from '@osac/types';
import type { Tenant } from '@osac/types/private';

import { SubnetsListPage } from './SubnetsListPage';
import type { UserRole } from '../../shellTypes';
import { renderWithProviders } from '../../test-utils/TestProviders';

const virtualNetworks = [
  {
    id: 'vn-1',
    metadata: { name: 'vn-prod', project: 'proj-a', tenant: 'tenant-a' },
    spec: { ipv4Cidr: '10.0.0.0/16' },
    status: { state: VirtualNetworkState.READY },
  },
  {
    id: 'vn-2',
    metadata: { name: 'vn-dev', project: 'proj-a' },
    spec: { ipv4Cidr: '10.1.0.0/16' },
    status: { state: VirtualNetworkState.READY },
  },
] as VirtualNetwork[];

const subnets = [
  {
    id: 'subnet-1',
    metadata: { name: 'subnet-web', tenant: 'tenant-a' },
    spec: { virtualNetwork: { id: 'vn-1' }, ipv4Cidr: '10.0.1.0/24' },
    status: { state: SubnetState.READY },
  },
  {
    id: 'subnet-2',
    metadata: { name: 'subnet-dev', tenant: 'tenant-b' },
    spec: { virtualNetwork: { id: 'vn-2' }, ipv4Cidr: '10.1.1.0/24' },
    status: { state: SubnetState.FAILED, message: 'CIDR outside the address plan' },
  },
  {
    id: 'subnet-default',
    metadata: {
      name: 'default',
      tenant: 'tenant-a',
      labels: { 'osac.openshift.io/default': 'true' },
    },
    spec: { virtualNetwork: { id: 'vn-1' }, ipv4Cidr: '10.0.0.0/24' },
    status: { state: SubnetState.READY },
  },
] as unknown as Subnet[];

const tenants = [
  { id: 'tenant-a', metadata: { name: 'Acme' } },
  { id: 'tenant-b', metadata: { name: 'Globex' } },
] as Tenant[];

const renderPage = (
  role: UserRole = 'tenant-user',
  routerEntries = ['/networking/subnets'],
  fixtureSubnets = subnets,
) =>
  renderWithProviders(<SubnetsListPage />, {
    apiFixtures: { virtualNetworks, subnets: fixtureSubnets, tenants },
    routerEntries,
    session: { role },
  });

describe('SubnetsListPage', () => {
  it('lists subnets with status, CIDR and a link to the virtual network', async () => {
    renderPage();

    expect(await screen.findByText('subnet-web')).toBeInTheDocument();
    expect(screen.getAllByRole('columnheader').map((header) => header.textContent)).toEqual([
      'Name',
      'Status',
      'IPv4 CIDR',
      'Virtual network',
      'Project',
      '',
    ]);
    const row = screen.getByText('subnet-web').closest('tr') as HTMLElement;
    expect(within(row).getByText('10.0.1.0/24')).toBeInTheDocument();
    expect(within(row).getByRole('link', { name: 'vn-prod' })).toHaveAttribute(
      'href',
      '/networking/virtual-networks/vn-1',
    );
    expect(screen.queryByRole('link', { name: 'subnet-web' })).not.toBeInTheDocument();
  });

  it('shows the Default badge and a disabled Delete on the default subnet', async () => {
    const { user } = renderPage();

    const row = (await screen.findByText('default')).closest('tr') as HTMLElement;
    expect(within(row).getByText('Default')).toBeInTheDocument();
    await user.click(within(row).getByRole('button', { name: /Actions for/ }));
    const deleteItem = screen.getByRole('menuitem', { name: 'Delete subnet' });
    expect(deleteItem).toHaveAttribute('aria-disabled', 'true');
    await user.hover(deleteItem);
    expect(
      await screen.findByText('Created as a tenant default. It is deleted with the tenant.'),
    ).toBeInTheDocument();
  });

  it('shows the status message in a popover', async () => {
    const { user } = renderPage();

    await user.click(await screen.findByRole('button', { name: 'Failed' }));

    expect(await screen.findByText('CIDR outside the address plan')).toBeInTheDocument();
  });

  it('filters by the virtual network in the URL', async () => {
    renderPage('tenant-user', ['/networking/subnets?virtualNetwork=vn-2']);

    expect(await screen.findByText('subnet-dev')).toBeInTheDocument();
    expect(screen.queryByText('subnet-web')).not.toBeInTheDocument();
    expect(screen.getByText('1 of 3 shown')).toBeInTheDocument();
  });

  it('pre-fills the create modal from the virtual network filter', async () => {
    const { user } = renderPage('tenant-user', ['/networking/subnets?virtualNetwork=vn-2']);

    await screen.findByText('subnet-dev');
    await user.click(screen.getByRole('button', { name: 'Create subnet' }));

    const dialog = within(await screen.findByRole('dialog'));
    await waitFor(() =>
      expect(dialog.getByLabelText(/^Virtual network/)).toHaveTextContent('vn-dev'),
    );
    expect(dialog.getByLabelText(/^Virtual network/)).toBeEnabled();
  });

  it('shows the empty state and the no-results state', async () => {
    const { user, unmount } = renderPage('tenant-user', ['/networking/subnets'], []);

    expect(await screen.findByRole('heading', { name: 'No subnets' })).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Create subnet' })).toHaveLength(2);
    unmount();

    renderPage('tenant-user', ['/networking/subnets?search=nothing']);
    expect(await screen.findByRole('heading', { name: 'No results found' })).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Clear all filters' })).toHaveLength(1);
    await user.click(screen.getByRole('button', { name: 'Clear all filters' }));
    expect(await screen.findByText('subnet-web')).toBeInTheDocument();
  });

  it('names the tenant of each virtual network in the filter for a provider admin', async () => {
    const { user } = renderPage('admin');

    await screen.findByText('subnet-web');
    await user.click(screen.getByRole('button', { name: 'Filter by virtual network' }));
    expect(await screen.findByRole('option', { name: 'vn-prod (Acme)' })).toBeInTheDocument();
  });

  it('names only the virtual network in the filter for a tenant user', async () => {
    const { user } = renderPage('tenant-user');

    await screen.findByText('subnet-web');
    await user.click(screen.getByRole('button', { name: 'Filter by virtual network' }));
    expect(await screen.findByRole('option', { name: 'vn-prod' })).toBeInTheDocument();
  });

  it('shows the Tenant column and filter only for a provider admin', async () => {
    const { user } = renderPage('admin');

    expect(await screen.findByRole('columnheader', { name: 'Tenant' })).toBeInTheDocument();
    expect(await screen.findAllByText('Acme')).toHaveLength(2);

    await user.click(screen.getByRole('button', { name: 'Filter by tenant' }));
    await user.click(screen.getByRole('option', { name: 'Globex' }));

    expect(screen.getByText('subnet-dev')).toBeInTheDocument();
    expect(screen.queryByText('subnet-web')).not.toBeInTheDocument();
  });

  it('hides the Tenant column and filter for a tenant user', async () => {
    renderPage('tenant-user');

    await screen.findByText('subnet-web');
    expect(screen.queryByRole('columnheader', { name: 'Tenant' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Filter by tenant' })).not.toBeInTheDocument();
  });

  it('shows each subnet project, linked to the project, and filters by project', async () => {
    const projects = [
      {
        id: 'p-root',
        metadata: { name: '' },
        status: { state: ProjectState.ACTIVE },
      },
      {
        id: 'p-team',
        metadata: { name: 'team-a' },
        spec: { title: 'Team A' },
        status: { state: ProjectState.ACTIVE },
      },
    ] as Project[];
    const projectSubnets = [
      { ...subnets[0], metadata: { name: 'subnet-web', project: 'team-a' } },
      { ...subnets[1], metadata: { name: 'subnet-dev', project: '' } },
    ] as Subnet[];
    const { user } = renderWithProviders(<SubnetsListPage />, {
      apiFixtures: { virtualNetworks, subnets: projectSubnets, projects },
      routerEntries: ['/networking/subnets?project=team-a&search=subnet'],
      session: { role: 'tenant-user' },
    });

    const row = (await screen.findByText('subnet-web')).closest('tr') as HTMLElement;
    expect(await within(row).findByRole('link', { name: 'Team A' })).toHaveAttribute(
      'href',
      '/projects/p-team',
    );
    expect(screen.queryByText('subnet-dev')).not.toBeInTheDocument();

    // Clearing every filter at once clears the project and the search together.
    await user.click(screen.getByRole('button', { name: 'Clear all filters' }));
    const rootRow = (await screen.findByText('subnet-dev')).closest('tr') as HTMLElement;
    expect(await within(rootRow).findByRole('link', { name: 'Root' })).toHaveAttribute(
      'href',
      '/projects/p-root',
    );
  });

  it('disables Delete while a workload uses the subnet', async () => {
    const computeInstances = [
      {
        id: 'vm-1',
        metadata: { name: 'web-1' },
        spec: { networkAttachments: [{ subnet: { id: 'subnet-1' } }] },
      },
    ] as unknown as ComputeInstance[];
    const clusters = [
      {
        id: 'c-1',
        metadata: { name: 'c1' },
        spec: { networkAttachment: { subnet: { id: 'subnet-1' } } },
      },
    ] as unknown as Cluster[];
    const { user } = renderWithProviders(<SubnetsListPage />, {
      apiFixtures: { virtualNetworks, subnets, tenants, computeInstances, clusters },
      routerEntries: ['/networking/subnets'],
      session: { role: 'tenant-user' },
    });

    await user.click(await screen.findByRole('button', { name: 'Actions for subnet-web' }));
    const deleteItem = screen.getByRole('menuitem', { name: 'Delete subnet' });
    await waitFor(() => expect(deleteItem).toHaveAttribute('aria-disabled', 'true'));
    await user.hover(deleteItem);
    expect(
      await screen.findByText('Used by virtual machine web-1 and 1 more. Delete them first.'),
    ).toBeInTheDocument();
  });

  it('ignores workloads of services that are not enabled', async () => {
    const clusters = [
      {
        id: 'c-1',
        metadata: { name: 'c1' },
        spec: { networkAttachment: { subnet: { id: 'subnet-1' } } },
      },
    ] as unknown as Cluster[];
    const onClusterList = vi.fn();
    const onComputeInstanceList = vi.fn();
    const { user } = renderWithProviders(<SubnetsListPage />, {
      apiFixtures: {
        virtualNetworks,
        subnets,
        tenants,
        clusters,
        enabledServices: [ServiceTier.VMAAS],
      },
      transportOverrides: { onClusterList, onComputeInstanceList },
      routerEntries: ['/networking/subnets'],
      session: { role: 'tenant-user' },
    });

    // The enabled service's workloads are loaded; the disabled one's are never asked for.
    await waitFor(() => expect(onComputeInstanceList).toHaveBeenCalled());
    expect(onClusterList).not.toHaveBeenCalled();
    await user.click(await screen.findByRole('button', { name: 'Actions for subnet-web' }));
    expect(screen.getByRole('menuitem', { name: 'Delete subnet' })).not.toHaveAttribute(
      'aria-disabled',
      'true',
    );
  });
});
