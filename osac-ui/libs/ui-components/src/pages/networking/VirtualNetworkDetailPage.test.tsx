import { Route, Routes, useLocation } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type {
  ExternalIP,
  FabricDomain,
  NATGateway,
  SecurityGroup,
  Subnet,
  VirtualNetwork,
} from '@osac/types';
import {
  ExternalIPState,
  NATGatewayState,
  NATGatewayStatusSchema,
  Protocol,
  SecurityGroupState,
  ServiceTier,
  SubnetState,
  VirtualNetworkLocalReferenceSchema,
  VirtualNetworkState,
} from '@osac/types';

import AttachNatGatewayWizardPage from './AttachNatGatewayWizardPage';
import { VirtualNetworkDetailPage } from './VirtualNetworkDetailPage';
import type {
  MockApiFixtures,
  MockTransportOverrides,
} from '../../test-utils/createMockConnectTransport';
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

const defaultLabels = { 'osac.openshift.io/default': 'true' };

const CurrentSearch = () => <p>{`Search: ${useLocation().search}`}</p>;

const renderPage = (
  fixtures: MockApiFixtures = {},
  transportOverrides: MockTransportOverrides = {},
  routerEntries = ['/networking/virtual-networks/vn-1'],
) =>
  renderWithProviders(
    <Routes>
      <Route path="/networking/virtual-networks" element={<h1>Virtual networks list</h1>} />
      <Route
        path="/networking/virtual-networks/:id/nat-gateway/attach"
        element={<AttachNatGatewayWizardPage />}
      />
      <Route
        path="/networking/virtual-networks/:id"
        element={
          <>
            <VirtualNetworkDetailPage />
            <CurrentSearch />
          </>
        }
      />
    </Routes>,
    {
      routerEntries,
      apiFixtures: {
        virtualNetworks: [mockVN],
        subnets: mockSubnets,
        securityGroups: mockSecurityGroups,
        ...fixtures,
      },
      transportOverrides,
      session: { role: 'tenant-admin' },
    },
  );

const openTab = async (user: ReturnType<typeof renderPage>['user'], name: string) => {
  await user.click(await screen.findByRole('tab', { name }));
  return within(screen.getByRole('tabpanel'));
};

describe('VirtualNetworkDetailPage', () => {
  it('renders the Overview, Subnets, Security groups and NAT gateway tabs', async () => {
    renderPage();

    expect(await screen.findByRole('heading', { name: 'vn-prod' })).toBeInTheDocument();
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual([
      'Overview',
      'Subnets',
      'Security groups',
      'NAT gateway',
    ]);
    expect(screen.getByRole('link', { name: 'Virtual networks' })).toHaveAttribute(
      'href',
      '/networking/virtual-networks',
    );
    expect(screen.getByText('10.0.0.0/16')).toBeInTheDocument();
    expect(screen.getByText('Created')).toBeInTheDocument();
  });

  it('names the root project Root in the overview, as the rest of the console does', async () => {
    renderPage();

    const overview = within(await screen.findByRole('tabpanel', { name: 'Overview' }));
    expect(overview.getByText('Project')).toBeInTheDocument();
    expect(overview.getByText('Root')).toBeInTheDocument();
  });

  it('keeps the active tab in the URL', async () => {
    const { user } = renderPage({}, {}, ['/networking/virtual-networks/vn-1?tab=nat-gateway']);

    expect(await screen.findByRole('tab', { name: 'NAT gateway' })).toHaveAttribute(
      'aria-selected',
      'true',
    );
    await user.click(screen.getByRole('tab', { name: 'Subnets' }));
    expect(screen.getByText('Search: ?tab=subnets')).toBeInTheDocument();
    await user.click(screen.getByRole('tab', { name: 'Overview' }));
    expect(screen.getByText('Search:')).toBeInTheDocument();
  });

  it('renders the security groups scoped to this virtual network', async () => {
    const { user } = renderPage();

    const tab = await openTab(user, 'Security groups');
    expect(await tab.findByRole('link', { name: 'sg-web' })).toBeInTheDocument();
  });

  it('shows an empty state with Create when there are no security groups', async () => {
    const { user } = renderPage({ securityGroups: [] });

    const tab = await openTab(user, 'Security groups');
    expect(await tab.findByRole('heading', { name: 'No security groups' })).toBeInTheDocument();
    await user.click(tab.getByRole('button', { name: 'Create security group' }));
    expect(screen.getByRole('heading', { name: 'Create security group' })).toBeInTheDocument();
  });

  it('says why Create is unavailable on an empty tab of a network that is not ready', async () => {
    const { user } = renderPage({
      subnets: [],
      virtualNetworks: [
        { ...mockVN, status: { state: VirtualNetworkState.PENDING } } as VirtualNetwork,
      ],
    });

    const tab = await openTab(user, 'Subnets');
    expect(await tab.findByRole('heading', { name: 'No subnets' })).toBeInTheDocument();
    expect(tab.getByText('Available when the virtual network is Ready.')).toBeInTheDocument();
    expect(tab.getByRole('button', { name: 'Create subnet' })).toHaveAttribute(
      'aria-disabled',
      'true',
    );
  });

  it('opens the subnet create modal with the virtual network locked', async () => {
    const { user } = renderPage();

    const tab = await openTab(user, 'Subnets');
    await user.click(tab.getByRole('button', { name: 'Create subnet' }));

    expect(screen.getByRole('heading', { name: 'Create subnet' })).toBeInTheDocument();
    const dialog = within(screen.getByRole('dialog'));
    await waitFor(() =>
      expect(dialog.getByLabelText(/^Virtual network/)).toHaveTextContent(/vn-prod/),
    );
    expect(dialog.getByLabelText(/^Virtual network/)).toBeDisabled();
  });

  it.each(['Subnets', 'Security groups'])(
    'disables Create on the %s tab with a reason until the network is ready',
    async (tabName) => {
      const { user } = renderPage({
        virtualNetworks: [
          { ...mockVN, status: { state: VirtualNetworkState.PENDING } } as VirtualNetwork,
        ],
      });

      const tab = await openTab(user, tabName);
      const createButton = await tab.findByRole('button', { name: /^Create/ });
      expect(createButton).toHaveAttribute('aria-disabled', 'true');
      await user.click(createButton);
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      // The button stays focusable, so the reason is reachable from the keyboard.
      createButton.focus();
      expect(
        await screen.findByText('Available when the virtual network is Ready.'),
      ).toBeInTheDocument();
    },
  );

  it('disables Create NAT gateway until the network is ready', async () => {
    const { user } = renderPage({
      virtualNetworks: [
        { ...mockVN, status: { state: VirtualNetworkState.PENDING } } as VirtualNetwork,
      ],
    });

    const tab = await openTab(user, 'NAT gateway');
    const createButton = await tab.findByRole('button', { name: 'Create NAT gateway' });
    expect(createButton).toHaveAttribute('aria-disabled', 'true');
    await user.hover(createButton);
    expect(
      await screen.findByText('Available when the virtual network is Ready.'),
    ).toBeInTheDocument();
  });

  it('hides Create NAT gateway on the tenant default network', async () => {
    const { user } = renderPage({
      virtualNetworks: [
        { ...mockVN, metadata: { ...mockVN.metadata, labels: defaultLabels } } as VirtualNetwork,
      ],
    });

    const tab = await openTab(user, 'NAT gateway');
    expect(
      await tab.findByText('No NAT gateway associated with this virtual network.'),
    ).toBeInTheDocument();
    expect(tab.queryByRole('button', { name: 'Create NAT gateway' })).not.toBeInTheDocument();
  });

  it('shows the message of a FAILED network once, in the alert', async () => {
    renderPage({
      virtualNetworks: [
        {
          ...mockVN,
          status: { state: VirtualNetworkState.FAILED, message: 'CIDR exceeds the address plan' },
        } as VirtualNetwork,
      ],
    });

    expect(await screen.findByText('Provisioning failed')).toBeInTheDocument();
    expect(screen.getAllByText('CIDR exceeds the address plan')).toHaveLength(1);
    expect(screen.queryByRole('button', { name: 'Failed' })).not.toBeInTheDocument();
  });

  it('shows the status message of a READY network in a popover on its label', async () => {
    const { user } = renderPage({
      virtualNetworks: [
        {
          ...mockVN,
          status: { state: VirtualNetworkState.READY, message: 'Provider networking is disabled' },
        } as VirtualNetwork,
      ],
    });

    await user.click(await screen.findByRole('button', { name: 'Ready' }));
    expect(await screen.findByText('Status details')).toBeInTheDocument();
    expect(screen.getByText('Provider networking is disabled')).toBeInTheDocument();
  });

  it('opens the create modal with the current virtual network pre-selected and locked', async () => {
    const { user } = renderPage();

    const tab = await openTab(user, 'Security groups');
    await user.click(tab.getByRole('button', { name: /Create security group/i }));

    expect(screen.getByRole('heading', { name: 'Create security group' })).toBeInTheDocument();
    const vnField = within(screen.getByRole('dialog')).getByLabelText(/^Virtual network/);
    expect(vnField).toHaveTextContent(/vn-prod/i);
    expect(vnField.closest('button')).toBeDisabled();
  });

  it('shows an empty NAT gateway section with Create', async () => {
    const { user } = renderPage();

    const tab = await openTab(user, 'NAT gateway');
    expect(
      await tab.findByText('No NAT gateway associated with this virtual network.'),
    ).toBeInTheDocument();
    await user.click(tab.getByRole('button', { name: 'Create NAT gateway' }));
    expect(await screen.findByRole('heading', { name: 'Create NAT gateway' })).toBeInTheDocument();
  });

  it('shows attached NAT gateway name, address, status, and delete', async () => {
    const { user } = renderPage({
      natGateways: [attachedNat],
      externalIps: [attachedIp],
    });

    await openTab(user, 'NAT gateway');
    expect(await screen.findByText('nat-egress')).toBeInTheDocument();
    expect(
      within(screen.getByRole('tabpanel')).getByText('NAT gateway').closest('.pf-m-secondary'),
    ).toBeInTheDocument();
    expect(screen.getByText('203.0.113.10')).toBeInTheDocument();
    expect(screen.getAllByText('Ready').length).toBeGreaterThan(0);
    expect(screen.getByText('203.0.113.10').closest('code')).toBeInTheDocument();
    const natDetails = within(screen.getByLabelText('NAT gateway details'));
    expect(natDetails.getByText('Created')).toBeInTheDocument();
    expect(natDetails.queryByText('Attached')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Actions for nat-egress' }));
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

    await openTab(user, 'NAT gateway');
    await screen.findByText('nat-egress');
    await user.click(screen.getByRole('button', { name: 'Actions for nat-egress' }));
    const deleteItem = screen.getByRole('menuitem', { name: 'Delete' });
    expect(deleteItem).toHaveAttribute('aria-disabled', 'true');
    expect(deleteItem).not.toBeDisabled();

    await user.click(deleteItem);
    expect(screen.queryByRole('heading', { name: /Delete nat-egress\?/ })).not.toBeInTheDocument();
  });

  it('disables Delete with the known dependents as the reason', async () => {
    const { user } = renderPage({ natGateways: [attachedNat], externalIps: [attachedIp] });

    await screen.findByText(/nat-egress/);
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Delete virtual network' })).toHaveAttribute(
        'aria-disabled',
        'true',
      ),
    );
    const deleteButton = screen.getByRole('button', { name: 'Delete virtual network' });
    await user.click(deleteButton);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    deleteButton.focus();
    expect(
      await screen.findByText(
        'Delete its dependents first: subnets (1), security groups (1), NAT gateway nat-egress.',
      ),
    ).toBeInTheDocument();
  });

  it('deletes a network without known dependents', async () => {
    const onVirtualNetworkDelete = vi.fn();
    const { user } = renderPage({ subnets: [], securityGroups: [] }, { onVirtualNetworkDelete });

    await screen.findByRole('heading', { name: 'vn-prod' });
    const deleteButton = screen.getByRole('button', { name: 'Delete virtual network' });
    await waitFor(() => expect(deleteButton).not.toHaveAttribute('aria-disabled', 'true'));
    await user.click(deleteButton);

    const dialog = within(screen.getByRole('dialog'));
    expect(dialog.getByText('Delete vn-prod?')).toBeInTheDocument();
    await user.click(dialog.getByRole('button', { name: 'Delete' }));

    await waitFor(() =>
      expect(onVirtualNetworkDelete).toHaveBeenCalledWith(expect.objectContaining({ id: 'vn-1' })),
    );
    expect(
      await screen.findByRole('heading', { name: 'Virtual networks list' }),
    ).toBeInTheDocument();
  });

  it('disables Delete while the network has fabric domains and bare metal is enabled', async () => {
    const fabricDomains = [{ id: 'fd-1', spec: { virtualNetwork: 'vn-1' } }] as FabricDomain[];
    const { user } = renderPage({ subnets: [], securityGroups: [], fabricDomains });

    const deleteButton = await screen.findByRole('button', { name: 'Delete virtual network' });
    await waitFor(() => expect(deleteButton).toHaveAttribute('aria-disabled', 'true'));
    await user.hover(deleteButton);
    expect(
      await screen.findByText('Delete its dependents first: fabric domains (1).'),
    ).toBeInTheDocument();
  });

  it('does not count fabric domains when bare metal is not enabled', async () => {
    const fabricDomains = [{ id: 'fd-1', spec: { virtualNetwork: 'vn-1' } }] as FabricDomain[];
    const onFabricDomainList = vi.fn();
    renderPage(
      {
        subnets: [],
        securityGroups: [],
        fabricDomains,
        enabledServices: [ServiceTier.VMAAS],
      },
      { onFabricDomainList },
    );

    const deleteButton = await screen.findByRole('button', { name: 'Delete virtual network' });
    await waitFor(() => expect(deleteButton).not.toHaveAttribute('aria-disabled', 'true'));
    expect(onFabricDomainList).not.toHaveBeenCalled();
  });

  it('keeps the delete confirmation open with the server error', async () => {
    const { user } = renderPage(
      { subnets: [], securityGroups: [] },
      {
        onVirtualNetworkDelete: () => {
          throw new ConnectError('virtual network has 1 subnet', Code.FailedPrecondition);
        },
      },
    );

    const deleteButton = await screen.findByRole('button', { name: 'Delete virtual network' });
    await waitFor(() => expect(deleteButton).not.toHaveAttribute('aria-disabled', 'true'));
    await user.click(deleteButton);
    const dialog = within(screen.getByRole('dialog'));
    await user.click(dialog.getByRole('button', { name: 'Delete' }));

    expect(await dialog.findByText('Failed to delete virtual network')).toBeInTheDocument();
    expect(dialog.getByText(/virtual network has 1 subnet/)).toBeInTheDocument();
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });

  it('shows the Default badge and disables Delete on the tenant default network', async () => {
    const { user } = renderPage({
      virtualNetworks: [
        { ...mockVN, metadata: { ...mockVN.metadata, labels: defaultLabels } } as VirtualNetwork,
      ],
    });

    expect(await screen.findByRole('heading', { name: 'vn-prod' })).toBeInTheDocument();
    expect(await screen.findByText('Default')).toBeInTheDocument();
    const deleteButton = screen.getByRole('button', { name: 'Delete virtual network' });
    expect(deleteButton).toHaveAttribute('aria-disabled', 'true');
    await user.hover(deleteButton);
    expect(
      await screen.findByText('Created as a tenant default. It is deleted with the tenant.'),
    ).toBeInTheDocument();
  });

  it('deletes a subnet from its row action', async () => {
    const onSubnetDelete = vi.fn();
    const { user } = renderPage({}, { onSubnetDelete });

    await openTab(user, 'Subnets');
    await user.click(await screen.findByRole('button', { name: 'Actions for subnet-a' }));
    await user.click(screen.getByRole('menuitem', { name: 'Delete subnet' }));

    const dialog = within(screen.getByRole('dialog'));
    expect(dialog.getByText('Delete subnet-a?')).toBeInTheDocument();
    await user.click(dialog.getByRole('button', { name: 'Delete' }));

    await waitFor(() =>
      expect(onSubnetDelete).toHaveBeenCalledWith(expect.objectContaining({ id: 'subnet-1' })),
    );
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  });

  it('shows the Default badge and a disabled Delete on a default subnet', async () => {
    const { user } = renderPage({
      subnets: [
        { ...mockSubnets[0], metadata: { ...mockSubnets[0].metadata, labels: defaultLabels } },
      ] as Subnet[],
    });

    await openTab(user, 'Subnets');
    expect(await screen.findByText('subnet-a')).toBeInTheDocument();
    expect(screen.getByText('Default')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Actions for subnet-a' }));
    expect(screen.getByRole('menuitem', { name: 'Delete subnet' })).toHaveAttribute(
      'aria-disabled',
      'true',
    );
  });

  it('disables Delete on the default NAT gateway', async () => {
    const { user } = renderPage({
      natGateways: [
        { ...attachedNat, metadata: { ...attachedNat.metadata, labels: defaultLabels } },
      ] as NATGateway[],
      externalIps: [attachedIp],
    });

    await openTab(user, 'NAT gateway');
    expect(await screen.findByText('nat-egress')).toBeInTheDocument();
    expect(screen.getByText('Default')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Actions for nat-egress' }));
    const deleteItem = screen.getByRole('menuitem', { name: 'Delete' });
    expect(deleteItem).toHaveAttribute('aria-disabled', 'true');
    await user.hover(deleteItem);
    expect(
      await screen.findByText('Created as a tenant default. It is deleted with the tenant.'),
    ).toBeInTheDocument();
  });
});
