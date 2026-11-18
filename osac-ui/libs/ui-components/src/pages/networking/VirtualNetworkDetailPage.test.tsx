import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { screen, within } from '@testing-library/react';
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
  metadata: { name: 'vn-prod', project: '', labels: {} },
  spec: { ipv4Cidr: '10.0.0.0/16' },
  status: { state: VirtualNetworkState.READY },
} as VirtualNetwork;

const defaultVN = {
  ...mockVN,
  id: 'vn-default',
  metadata: { name: 'default', labels: { 'osac.openshift.io/default': 'true' } },
} as unknown as VirtualNetwork;

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

const renderPage = (
  fixtures: MockApiFixtures = {},
  path = '/networking/virtual-networks/vn-1',
  transportOverrides = {},
) =>
  renderWithProviders(
    <Routes>
      <Route
        path="/networking/virtual-networks/:id/nat-gateway/attach"
        element={<AttachNatGatewayWizardPage />}
      />
      <Route path="/networking/virtual-networks/:id" element={<VirtualNetworkDetailPage />} />
    </Routes>,
    {
      routerEntries: [path],
      apiFixtures: {
        virtualNetworks: [mockVN, defaultVN],
        subnets: mockSubnets,
        securityGroups: mockSecurityGroups,
        ...fixtures,
      },
      transportOverrides,
    },
  );

describe('VirtualNetworkDetailPage', () => {
  it('shows the overview details, project Root and dependent counts', async () => {
    renderPage();

    expect(await screen.findByRole('heading', { name: 'vn-prod' })).toBeInTheDocument();
    expect(screen.getByText('Root')).toBeInTheDocument();
    expect(screen.getByText('10.0.0.0/16')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Overview' })).toHaveAttribute('aria-selected', 'true');
  });

  it('links each subnet to its details page', async () => {
    const { user } = renderPage();

    await user.click(await screen.findByRole('tab', { name: 'Subnets' }));
    expect(screen.getByRole('link', { name: 'subnet-a' })).toHaveAttribute(
      'href',
      '/networking/subnets/subnet-1',
    );
  });

  it('renders the Unknown status instead of a literal 0', async () => {
    renderPage(
      {
        virtualNetworks: [
          mockVN,
          defaultVN,
          {
            ...mockVN,
            id: 'vn-unknown',
            status: { state: VirtualNetworkState.UNSPECIFIED },
          } as unknown as VirtualNetwork,
        ],
      },
      '/networking/virtual-networks/vn-unknown',
    );

    expect(await screen.findByText('Unknown')).toBeInTheDocument();
  });

  it('disables Delete for an automatically created NAT gateway', async () => {
    renderPage({
      natGateways: [
        {
          ...attachedNat,
          metadata: {
            name: 'nat-egress',
            labels: { 'osac.openshift.io/auto-created-for': 'virtual-network/vn-1' },
          },
        } as unknown as NATGateway,
      ],
      externalIps: [attachedIp],
    });

    expect(await screen.findByText('nat-egress')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete' })).toHaveAttribute('aria-disabled', 'true');
  });

  it('disables Delete for a default NAT gateway', async () => {
    renderPage({
      natGateways: [
        {
          ...attachedNat,
          metadata: { name: 'nat-egress', labels: { 'osac.openshift.io/default': 'true' } },
        } as unknown as NATGateway,
      ],
      externalIps: [attachedIp],
    });

    expect(await screen.findByText('nat-egress')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete' })).toHaveAttribute('aria-disabled', 'true');
  });

  it('shows the subnets in the Subnets tab', async () => {
    const { user } = renderPage();

    await user.click(await screen.findByRole('tab', { name: 'Subnets' }));
    const tabPanel = screen.getByRole('tabpanel');
    expect(within(tabPanel).getByText('subnet-a')).toBeInTheDocument();
    expect(within(tabPanel).getByText('10.0.1.0/24')).toBeInTheDocument();
  });

  it('shows the security groups in the Security groups tab and creates one there', async () => {
    const { user } = renderPage();

    await user.click(await screen.findByRole('tab', { name: 'Security groups' }));
    const tabPanel = screen.getByRole('tabpanel');
    expect(within(tabPanel).getByRole('link', { name: 'sg-web' })).toBeInTheDocument();

    await user.click(within(tabPanel).getByRole('button', { name: /Create security group/i }));

    expect(screen.getByRole('heading', { name: 'Create security group' })).toBeInTheDocument();
    const dialog = screen.getByRole('dialog');
    const vnField = within(dialog).getByLabelText(/Virtual Network/i);
    expect(vnField).toHaveTextContent(/vn-prod/i);
    expect(vnField.closest('button')).toBeDisabled();
  });

  it('shows a dash and Create NAT gateway when none is attached', async () => {
    renderPage();

    expect(await screen.findByRole('button', { name: 'Create NAT gateway' })).toBeInTheDocument();
    expect(screen.getAllByText('—').length).toBeGreaterThan(0);
  });

  it('hides Create NAT gateway and blocks Delete on a default network', async () => {
    renderPage({}, '/networking/virtual-networks/vn-default');

    await screen.findByRole('heading', { name: 'default' });
    expect(screen.queryByRole('button', { name: 'Create NAT gateway' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete virtual network' })).toHaveAttribute(
      'aria-disabled',
      'true',
    );
  });

  it('blocks Delete while the loaded dependents exist', async () => {
    renderPage();

    await screen.findByRole('heading', { name: 'vn-prod' });
    expect(screen.getByRole('button', { name: 'Delete virtual network' })).toHaveAttribute(
      'aria-disabled',
      'true',
    );
  });

  it('opens the delete confirmation when the network has no dependents', async () => {
    const { user } = renderPage({ subnets: [], securityGroups: [] });

    await screen.findByRole('heading', { name: 'vn-prod' });
    await user.click(screen.getByRole('button', { name: 'Delete virtual network' }));
    expect(screen.getByRole('dialog', { name: /Delete vn-prod\?/ })).toBeInTheDocument();
  });

  it('shows the attached NAT gateway name and address and deletes it', async () => {
    const { user } = renderPage({
      natGateways: [attachedNat],
      externalIps: [attachedIp],
    });

    expect(await screen.findByText('nat-egress')).toBeInTheDocument();
    expect(await screen.findByText(/203\.0\.113\.10/)).toBeInTheDocument();
    expect(screen.getAllByText('Ready').length).toBeGreaterThan(0);

    await user.click(screen.getByRole('button', { name: 'Delete' }));
    expect(screen.getByRole('dialog', { name: /Delete nat-egress\?/ })).toBeInTheDocument();
  });

  it('keeps the loaded NAT gateway visible when the address lookup fails', async () => {
    renderPage({ natGateways: [attachedNat] }, '/networking/virtual-networks/vn-1', {
      onExternalIpList: () => {
        throw new Error('addresses unavailable');
      },
    });

    expect(await screen.findByText('nat-egress')).toBeInTheDocument();
    expect(screen.getAllByText('Ready').length).toBeGreaterThan(0);
    expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument();
  });

  it('disables Delete while the NAT gateway is deleting', async () => {
    renderPage({
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

    expect(await screen.findByText('Deleting')).toBeInTheDocument();
  });
});
