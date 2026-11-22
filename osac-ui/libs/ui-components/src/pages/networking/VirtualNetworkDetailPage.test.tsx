import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type {
  ExternalIP,
  NATGateway,
  SecurityGroup,
  Subnet,
  VirtualNetwork,
  VirtualNetworksDeleteRequest,
} from '@osac/types';
import {
  ExternalIPState,
  NATGatewayState,
  NATGatewayStatusSchema,
  Protocol,
  SecurityGroupState,
  SubnetState,
  VirtualNetworkLocalReferenceSchema,
  VirtualNetworkState,
  VirtualNetworksDeleteResponseSchema,
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

const renderPage = (
  fixtures: MockApiFixtures = {},
  transportOverrides: MockTransportOverrides = {},
) =>
  renderWithProviders(
    <Routes>
      <Route
        path="/networking/virtual-networks/:id/nat-gateway/attach"
        element={<AttachNatGatewayWizardPage />}
      />
      <Route path="/networking/virtual-networks" element={<div>virtual networks list</div>} />
      <Route path="/networking/subnets/:id" element={<div>subnet details</div>} />
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
      transportOverrides,
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

  it('keeps the virtual network and its child rows read-only', async () => {
    renderPage();

    expect(await screen.findByText('subnet-a')).toBeInTheDocument();
    expect(await screen.findByRole('link', { name: 'sg-web' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Create subnet' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Create security group' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^(Edit|Update)/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: /^(Edit|Update)/ })).not.toBeInTheDocument();
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

  it('disables Delete for a default NAT gateway', async () => {
    const { user } = renderPage({
      natGateways: [
        {
          ...attachedNat,
          metadata: {
            ...attachedNat.metadata,
            labels: { 'osac.openshift.io/default': 'true' },
          },
        } as NATGateway,
      ],
      externalIps: [attachedIp],
    });

    await screen.findByText('nat-egress');
    await user.click(screen.getByRole('button', { name: 'Actions for NAT gateway attachment' }));

    const deleteItem = screen.getByRole('menuitem', { name: 'Delete' });
    expect(deleteItem).toHaveAttribute('aria-disabled', 'true');

    await user.click(deleteItem);
    expect(screen.queryByRole('heading', { name: /Delete nat-egress\?/ })).not.toBeInTheDocument();
  });

  it('links each subnet to its detail page', async () => {
    renderPage();

    expect(await screen.findByRole('link', { name: 'subnet-a' })).toHaveAttribute(
      'href',
      '/networking/subnets/subnet-1',
    );
  });

  describe('delete', () => {
    it('deletes the virtual network and returns to the list', async () => {
      let deleteRequest: VirtualNetworksDeleteRequest | undefined;
      const { user } = renderPage(
        {},
        {
          onVirtualNetworkDelete: (req) => {
            deleteRequest = req;
            return create(VirtualNetworksDeleteResponseSchema);
          },
        },
      );

      await screen.findByRole('heading', { name: 'vn-prod' });
      await user.click(screen.getByRole('button', { name: /^Delete$/ }));
      expect(screen.getByRole('heading', { name: /Delete vn-prod\?/ })).toBeInTheDocument();
      expect(screen.getByText(/Remove all dependent resources first/)).toBeInTheDocument();

      await user.click(
        within(screen.getByRole('dialog')).getByRole('button', { name: /^Delete$/ }),
      );

      await waitFor(() => expect(deleteRequest?.id).toBe('vn-1'));
      expect(await screen.findByText('virtual networks list')).toBeInTheDocument();
    });

    it('keeps the modal open and shows the server error when deletion fails', async () => {
      const { user } = renderPage(
        {},
        {
          onVirtualNetworkDelete: () => {
            throw new ConnectError(
              'cannot delete virtual network: referenced by Subnet subnet-1',
              Code.FailedPrecondition,
            );
          },
        },
      );

      await screen.findByRole('heading', { name: 'vn-prod' });
      await user.click(screen.getByRole('button', { name: /^Delete$/ }));
      await user.click(
        within(screen.getByRole('dialog')).getByRole('button', { name: /^Delete$/ }),
      );

      expect(await screen.findByText('Failed to delete virtual network')).toBeInTheDocument();
      expect(screen.getByText(/referenced by Subnet subnet-1/)).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: /Delete vn-prod\?/ })).toBeInTheDocument();
    });

    it('does not delete when Cancel is clicked', async () => {
      const onVirtualNetworkDelete = vi.fn();
      const { user } = renderPage({}, { onVirtualNetworkDelete });

      await screen.findByRole('heading', { name: 'vn-prod' });
      await user.click(screen.getByRole('button', { name: /^Delete$/ }));
      await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: /Cancel/ }));

      expect(onVirtualNetworkDelete).not.toHaveBeenCalled();
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    it('disables Delete until the virtual network is loaded', async () => {
      let resolveVnGet: ((value: { object?: VirtualNetwork }) => void) | undefined;
      const pendingVnGet = new Promise<{ object?: VirtualNetwork }>((resolve) => {
        resolveVnGet = resolve;
      });

      renderPage({}, { onVirtualNetworkGet: () => pendingVnGet });

      expect(screen.getByRole('button', { name: /^Delete$/ })).toHaveAttribute(
        'aria-disabled',
        'true',
      );

      resolveVnGet?.({ object: mockVN });
      await screen.findByRole('heading', { name: 'vn-prod' });
      await waitFor(() =>
        expect(screen.getByRole('button', { name: /^Delete$/ })).not.toHaveAttribute(
          'aria-disabled',
          'true',
        ),
      );
    });

    it('disables Delete for a default virtual network', async () => {
      renderPage({
        virtualNetworks: [
          {
            ...mockVN,
            metadata: {
              ...mockVN.metadata,
              labels: { ...mockVN.metadata?.labels, 'osac.openshift.io/default': 'true' },
            },
          } as VirtualNetwork,
        ],
      });

      await screen.findByRole('heading', { name: 'vn-prod' });
      await waitFor(() =>
        expect(screen.getByRole('button', { name: /^Delete$/ })).toHaveAttribute(
          'aria-disabled',
          'true',
        ),
      );
    });
  });
});
