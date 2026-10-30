import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { ExternalIP, NATGateway, SecurityGroup, Subnet, VirtualNetwork } from '@osac/types';
import {
  ExternalIPState,
  NATGatewayState,
  NATGatewayStatusSchema,
  Protocol,
  SecurityGroupState,
  SubnetState,
  VirtualNetworkLocalReferenceSchema,
  VirtualNetworkState,
} from '@osac/types';

import AttachNatGatewayWizardPage from './AttachNatGatewayWizardPage';
import { VirtualNetworkDetailPage } from './VirtualNetworkDetailPage';
import type { MockApiFixtures } from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

const mockVN = {
  id: 'vn-1',
  metadata: { name: 'vn-prod' },
  spec: { ipv4Cidr: '10.0.0.0/16' },
  status: { state: VirtualNetworkState.READY },
} as VirtualNetwork;

const mockSubnets = [
  {
    id: 'subnet-1',
    metadata: { name: 'subnet-a' },
    spec: { ipv4Cidr: '10.0.1.0/24', virtualNetwork: { id: 'vn-1' } },
    status: { state: SubnetState.READY },
  },
] as Subnet[];

const mockSecurityGroups: SecurityGroup[] = [
  {
    $typeName: 'osac.public.v1.SecurityGroup',
    id: 'sg-1',
    metadata: {
      $typeName: 'osac.public.v1.Metadata',
      displayName: '',
      description: '',
      name: 'sg-web',
      annotations: {},
      creator: 'foo',
      labels: {},
      project: 'foo',
      tenant: 'foo',
      version: 1,
    },
    spec: {
      $typeName: 'osac.public.v1.SecurityGroupSpec',
      virtualNetwork: create(VirtualNetworkLocalReferenceSchema, { id: 'vn-1' }),
      ingress: [
        {
          $typeName: 'osac.public.v1.SecurityRule',
          protocol: Protocol.TCP,
          portFrom: 80,
          portTo: 80,
        },
      ],
      egress: [],
    },
    status: {
      $typeName: 'osac.public.v1.SecurityGroupStatus',
      state: SecurityGroupState.READY,
    },
  },
];

const attachedNat = {
  id: 'nat-1',
  metadata: { name: 'nat-egress' },
  spec: { virtualNetwork: { id: 'vn-1' }, externalIp: { id: 'eip-1' } },
  status: { state: NATGatewayState.NAT_GATEWAY_STATE_READY },
} as NATGateway;

const attachedIp = {
  id: 'eip-1',
  metadata: { name: 'eip-1' },
  status: {
    state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED,
    attached: false,
    address: '203.0.113.10',
  },
} as ExternalIP;

const renderPage = (fixtures: MockApiFixtures = {}) =>
  renderWithProviders(
    <Routes>
      <Route
        path="/networking/virtual-networks/:id/nat-gateway/attach"
        element={<AttachNatGatewayWizardPage />}
      />
      <Route path="/networking/virtual-networks/:id" element={<VirtualNetworkDetailPage />} />
    </Routes>,
    {
      routerEntries: ['/networking/virtual-networks/vn-1'],
      apiFixtures: {
        virtualNetworks: [mockVN],
        subnets: mockSubnets,
        securityGroups: mockSecurityGroups,
        ...fixtures,
      },
    },
  );

describe('VirtualNetworkDetailPage', () => {
  it('renders the security groups scoped to this virtual network', async () => {
    renderPage();

    expect(await screen.findByText('Security groups')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'sg-web' })).toBeInTheDocument();
  });

  it('shows an empty state when there are no security groups', async () => {
    renderPage({ securityGroups: [] });

    expect(await screen.findByText(/No security groups yet/i)).toBeInTheDocument();
  });

  it('opens the create modal with the current virtual network pre-selected and locked', async () => {
    const { user } = renderPage();

    await user.click(await screen.findByRole('button', { name: /Create security group/i }));

    expect(screen.getByRole('heading', { name: 'Create security group' })).toBeInTheDocument();
    const vnField = screen.getByLabelText(/Virtual Network/i);
    expect(vnField).toHaveTextContent(/vn-prod/i);
    expect(vnField.closest('button')).toBeDisabled();
  });

  it('shows an empty NAT gateway section with Create', async () => {
    const { user } = renderPage();

    expect(
      await screen.findByText('No NAT gateway associated with this virtual network.'),
    ).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Create' }));
    expect(screen.getByRole('heading', { name: 'NAT gateway attachment' })).toBeInTheDocument();
  });

  it('shows attached NAT gateway name, address, status, and delete', async () => {
    const { user } = renderPage({
      natGateways: [attachedNat],
      externalIps: [attachedIp],
    });

    expect(await screen.findByText('nat-egress')).toBeInTheDocument();
    expect(
      screen.getByText('NAT gateway attachment').closest('.pf-m-secondary'),
    ).toBeInTheDocument();
    expect(screen.getByText('203.0.113.10')).toBeInTheDocument();
    expect(screen.getAllByText('Ready').length).toBeGreaterThan(0);
    expect(screen.getByText('203.0.113.10').closest('code')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Actions for NAT gateway attachment' }));
    await user.click(screen.getByRole('menuitem', { name: 'Delete' }));
    expect(screen.getByRole('heading', { name: /Delete nat-egress\?/ })).toBeInTheDocument();
  });

  it('disables Delete while the NAT gateway is deleting', async () => {
    const { user } = renderPage({
      natGateways: [
        {
          ...attachedNat,
          status: create(NATGatewayStatusSchema, {
            state: NATGatewayState.NAT_GATEWAY_STATE_DELETING,
          }),
        },
      ],
      externalIps: [attachedIp],
    });

    await screen.findByText('nat-egress');
    await user.click(screen.getByRole('button', { name: 'Actions for NAT gateway attachment' }));
    const deleteItem = screen.getByRole('menuitem', { name: 'Delete' });
    expect(deleteItem).toHaveAttribute('aria-disabled', 'true');
    expect(deleteItem).not.toBeDisabled();

    await user.click(deleteItem);
    expect(screen.queryByRole('heading', { name: /Delete nat-egress\?/ })).not.toBeInTheDocument();
  });
});
