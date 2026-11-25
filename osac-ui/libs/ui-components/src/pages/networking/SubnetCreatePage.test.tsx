import { Route, Routes } from 'react-router-dom';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { Subnet, SubnetsCreateRequest, VirtualNetwork } from '@osac/types';
import { SubnetState, VirtualNetworkState } from '@osac/types';

import { SubnetCreatePage } from './SubnetCreatePage';
import type { MockTransportOverrides } from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

const mockNetworks = [
  {
    id: 'vn-1',
    metadata: { name: 'app-net', project: 'web-team' },
    spec: { ipv4Cidr: '10.20.0.0/16' },
    status: { state: VirtualNetworkState.READY },
  },
  {
    id: 'vn-2',
    metadata: { name: 'pending-net', project: 'web-team' },
    spec: { ipv4Cidr: '10.40.0.0/16' },
    status: { state: VirtualNetworkState.PENDING },
  },
  {
    id: 'vn-default',
    metadata: {
      name: 'default',
      project: '',
      labels: { 'osac.openshift.io/default': 'true' },
    },
    spec: { ipv4Cidr: '10.100.0.0/20' },
    status: { state: VirtualNetworkState.READY },
  },
] as unknown as VirtualNetwork[];

const mockSubnets = [
  {
    id: 'subnet-a',
    metadata: { name: 'subnet-a', project: 'web-team' },
    spec: { ipv4Cidr: '10.20.0.0/24', virtualNetwork: { id: 'vn-1' } },
    status: { state: SubnetState.READY },
  },
  {
    id: 'subnet-default',
    metadata: {
      name: 'default',
      project: '',
      labels: { 'osac.openshift.io/default': 'true' },
    },
    spec: { ipv4Cidr: '10.100.0.0/24', virtualNetwork: { id: 'vn-default' } },
    status: { state: SubnetState.READY },
  },
] as unknown as Subnet[];

type RenderOptions = {
  virtualNetworks?: VirtualNetwork[];
  subnets?: Subnet[];
  routerEntries?: string[];
  transportOverrides?: MockTransportOverrides;
};

const renderPage = (options: RenderOptions = {}) =>
  renderWithProviders(
    <Routes>
      <Route path="/networking/subnets/create" element={<SubnetCreatePage />} />
      <Route path="/networking/subnets" element={<div>subnets list</div>} />
      <Route path="/networking/virtual-networks/:id" element={<div>network details</div>} />
    </Routes>,
    {
      apiFixtures: {
        virtualNetworks: options.virtualNetworks ?? mockNetworks,
        subnets: options.subnets ?? mockSubnets,
      },
      transportOverrides: options.transportOverrides,
      routerEntries: options.routerEntries ?? ['/networking/subnets/create'],
    },
  );

const nameInput = () => screen.getByRole('textbox', { name: 'Name' });
const cidrInput = () => screen.getByRole('textbox', { name: 'IPv4 CIDR' });

describe('SubnetCreatePage', () => {
  it('pre-fills and locks the network opened from a virtual network', async () => {
    renderPage({ routerEntries: ['/networking/subnets/create?virtualNetwork=vn-1'] });

    const toggle = (await screen.findByText('app-net (10.20.0.0/16)')).closest('button');
    expect(toggle).toBeDisabled();
    expect(screen.queryByText('Only ready virtual networks are listed.')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'app-net' })).toBeInTheDocument();
    expect(await screen.findByText('Must be inside 10.20.0.0/16.')).toBeInTheDocument();
    await waitFor(() => expect(cidrInput()).toHaveAttribute('placeholder', '10.20.1.0/24'));
  });

  it('offers only READY virtual networks', async () => {
    const { user } = renderPage();

    const toggle = (await screen.findByText('Select a virtual network')).closest('button');
    expect(screen.getByText('Only ready virtual networks are listed.')).toBeInTheDocument();
    await user.click(toggle as HTMLElement);

    expect(screen.getByRole('option', { name: 'app-net (10.20.0.0/16)' })).toBeInTheDocument();
    expect(
      screen.queryByRole('option', { name: 'pending-net (10.40.0.0/16)' }),
    ).not.toBeInTheDocument();
  });

  it('blocks Create while the tenant default subnet is not Ready', async () => {
    renderPage({
      routerEntries: ['/networking/subnets/create?virtualNetwork=vn-default'],
      subnets: [
        {
          ...mockSubnets[1],
          status: { state: SubnetState.PENDING },
        } as unknown as Subnet,
      ],
    });

    expect(
      await screen.findByText('Available when the default subnet is Ready.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Create' })).toHaveAttribute('aria-disabled', 'true');
  });

  it('allows Create once the tenant default subnet is Ready', async () => {
    renderPage({ routerEntries: ['/networking/subnets/create?virtualNetwork=vn-default'] });

    await screen.findByText('default (10.100.0.0/20)');
    expect(
      screen.queryByText('Available when the default subnet is Ready.'),
    ).not.toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Create' })).not.toHaveAttribute(
        'aria-disabled',
        'true',
      ),
    );
  });

  it('creates the subnet with the network id and project, then returns to the network', async () => {
    let createRequest: SubnetsCreateRequest | undefined;
    const { user } = renderPage({
      routerEntries: ['/networking/subnets/create?virtualNetwork=vn-1'],
      transportOverrides: {
        onSubnetCreate: (request) => {
          createRequest = request;
          return { object: { id: 'subnet-new', ...request.object } } as never;
        },
      },
    });

    await screen.findByText('app-net (10.20.0.0/16)');
    await user.type(nameInput(), 'web-c');
    await user.type(cidrInput(), '10.20.2.0/24');
    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(createRequest).toBeDefined());
    expect(createRequest?.object?.metadata?.name).toBe('web-c');
    expect(createRequest?.object?.metadata?.project).toBe('web-team');
    expect(createRequest?.object?.spec?.virtualNetwork?.id).toBe('vn-1');
    expect(createRequest?.object?.spec?.ipv4Cidr).toBe('10.20.2.0/24');
    expect(await screen.findByText('network details')).toBeInTheDocument();
  });

  it('shows the server message above the form and keeps the values', async () => {
    const { user } = renderPage({
      routerEntries: ['/networking/subnets/create?virtualNetwork=vn-1'],
      transportOverrides: {
        onSubnetCreate: () => {
          throw new ConnectError(
            'subnet CIDR 10.20.2.0/24 overlaps with an existing subnet',
            Code.FailedPrecondition,
          );
        },
      },
    });

    await screen.findByText('app-net (10.20.0.0/16)');
    await user.type(nameInput(), 'web-c');
    await user.type(cidrInput(), '10.20.2.0/24');
    await user.click(screen.getByRole('button', { name: 'Create' }));

    expect(await screen.findByText('Failed to create subnet')).toBeInTheDocument();
    expect(screen.getByText(/overlaps with an existing subnet/)).toBeInTheDocument();
    expect(nameInput()).toHaveValue('web-c');
    expect(cidrInput()).toHaveValue('10.20.2.0/24');
  });

  it('rejects a CIDR outside the parent network', async () => {
    const { user } = renderPage({
      routerEntries: ['/networking/subnets/create?virtualNetwork=vn-1'],
    });

    await screen.findByText('app-net (10.20.0.0/16)');
    await user.type(nameInput(), 'web-c');
    await user.type(cidrInput(), '10.99.0.0/24');
    await user.click(screen.getByRole('button', { name: 'Create' }));

    expect(
      await screen.findByText('CIDR must be within parent virtual network range'),
    ).toBeInTheDocument();
  });

  it('rejects a CIDR that overlaps an existing subnet', async () => {
    const { user } = renderPage({
      routerEntries: ['/networking/subnets/create?virtualNetwork=vn-1'],
    });

    await screen.findByText('app-net (10.20.0.0/16)');
    await user.type(nameInput(), 'web-c');
    await user.type(cidrInput(), '10.20.0.0/24');
    await user.click(screen.getByRole('button', { name: 'Create' }));

    expect(
      await screen.findByText(/CIDR overlaps with existing subnet "subnet-a"/),
    ).toBeInTheDocument();
  });

  it('reports a network with no free block instead of offering an example', async () => {
    const fullNetwork = {
      id: 'vn-full',
      metadata: { name: 'full-net', project: 'web-team' },
      spec: { ipv4Cidr: '10.50.0.0/24' },
      status: { state: VirtualNetworkState.READY },
    } as unknown as VirtualNetwork;
    const fullSubnet = {
      id: 'subnet-full',
      metadata: { name: 'full', project: 'web-team' },
      spec: { ipv4Cidr: '10.50.0.0/24', virtualNetwork: { id: 'vn-full' } },
      status: { state: SubnetState.READY },
    } as unknown as Subnet;

    renderPage({
      routerEntries: ['/networking/subnets/create?virtualNetwork=vn-full'],
      virtualNetworks: [...mockNetworks, fullNetwork],
      subnets: [...mockSubnets, fullSubnet],
    });

    await screen.findByText('full-net (10.50.0.0/24)');
    expect(
      await screen.findByText('No free block is available inside 10.50.0.0/24.'),
    ).toBeInTheDocument();
    expect(cidrInput()).not.toHaveAttribute('placeholder');
  });

  it('returns to where the create started on Cancel', async () => {
    const { user } = renderPage({
      routerEntries: ['/networking/subnets/create?virtualNetwork=vn-1'],
    });

    await screen.findByText('app-net (10.20.0.0/16)');
    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(await screen.findByText('network details')).toBeInTheDocument();
  });

  it('returns to the subnets list when the create started there', async () => {
    const { user } = renderPage();

    await screen.findByText('Select a virtual network');
    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(await screen.findByText('subnets list')).toBeInTheDocument();
  });
});
