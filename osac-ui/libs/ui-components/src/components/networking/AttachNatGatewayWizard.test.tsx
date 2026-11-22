import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { ExternalIP, NATGateway, NATGatewaysCreateRequest, VirtualNetwork } from '@osac/types';
import { ExternalIPState, NATGatewaysCreateResponseSchema } from '@osac/types';

import { AttachNatGatewayWizard } from './AttachNatGatewayWizard';
import type { MockTransportOverrides } from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

const virtualNetwork = {
  id: 'vn-1',
  metadata: { name: 'vn-prod' },
  spec: { ipv4Cidr: '10.0.0.0/16' },
} as VirtualNetwork;

const eligibleIp = {
  id: 'eip-free',
  metadata: { name: 'eip-free' },
  status: {
    state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED,
    attached: false,
    address: '203.0.113.10',
  },
} as ExternalIP;

const pendingIp = {
  id: 'eip-pending',
  metadata: { name: 'eip-pending' },
  status: {
    state: ExternalIPState.EXTERNAL_IP_STATE_PENDING,
    attached: false,
  },
} as ExternalIP;

const attachedIp = {
  id: 'eip-attached',
  metadata: { name: 'eip-attached' },
  status: {
    state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED,
    attached: true,
    address: '203.0.113.11',
  },
} as ExternalIP;

const natUsedIp = {
  id: 'eip-nat',
  metadata: { name: 'eip-nat' },
  status: {
    state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED,
    attached: false,
    address: '203.0.113.12',
  },
} as ExternalIP;

const existingNat = {
  id: 'nat-other',
  spec: {
    virtualNetwork: { id: 'vn-other' },
    externalIp: { id: 'eip-nat' },
  },
} as NATGateway;

const mixedIps = [eligibleIp, pendingIp, attachedIp, natUsedIp];

const renderModal = ({
  onClose = vi.fn(),
  externalIps = mixedIps,
  natGateways = [existingNat],
  transportOverrides,
}: {
  onClose?: () => void;
  externalIps?: ExternalIP[];
  natGateways?: NATGateway[];
  transportOverrides?: MockTransportOverrides;
} = {}) =>
  renderWithProviders(
    <AttachNatGatewayWizard virtualNetwork={virtualNetwork} onClose={onClose} />,
    {
      apiFixtures: { externalIps, natGateways },
      transportOverrides,
    },
  );

describe('AttachNatGatewayWizard', () => {
  it('renders the lede and read-only virtual network fields', async () => {
    renderModal();

    expect(screen.getByRole('heading', { name: 'NAT gateway attachment' })).toBeInTheDocument();
    expect(
      screen.getByText('Provides outbound internet access for workloads in this virtual network.'),
    ).toBeInTheDocument();
    expect(screen.getByText('vn-prod')).toBeInTheDocument();
    expect(screen.getByText('10.0.0.0/16')).toBeInTheDocument();
    expect(screen.getByLabelText(/^Name/)).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByLabelText(/^External IP/)).toBeEnabled();
    });
  });

  it('lists only unallocated External IPs with name and address labels', async () => {
    const { user } = renderModal();

    await waitFor(() => {
      expect(screen.getByLabelText(/^External IP/)).toBeEnabled();
    });
    await user.click(screen.getByLabelText(/^External IP/));

    const listbox = await screen.findByRole('listbox');
    expect(
      within(listbox).getByRole('option', { name: 'eip-free · 203.0.113.10' }),
    ).toBeInTheDocument();
    expect(within(listbox).queryByRole('option', { name: /eip-pending/ })).not.toBeInTheDocument();
    expect(within(listbox).queryByRole('option', { name: /eip-attached/ })).not.toBeInTheDocument();
    expect(within(listbox).queryByRole('option', { name: /eip-nat/ })).not.toBeInTheDocument();
  });

  it('shows a warning when no unallocated External IPs are available', async () => {
    renderModal({
      externalIps: [pendingIp, attachedIp, natUsedIp],
      natGateways: [existingNat],
    });

    expect(await screen.findByText('No unallocated external IPs')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled();
  });

  it('blocks attachment when the virtual network already has a NAT gateway', async () => {
    renderModal({
      natGateways: [
        {
          ...existingNat,
          id: 'nat-current-network',
          spec: { ...existingNat.spec, virtualNetwork: { id: 'vn-1' } },
        } as NATGateway,
      ],
    });

    expect(await screen.findByText('NAT gateway already attached')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled();
  });

  it('blocks attachment when NAT gateways cannot be loaded', async () => {
    renderModal({
      transportOverrides: {
        onNatGatewayList: () => {
          throw new ConnectError('NAT gateways unavailable', Code.Unavailable);
        },
      },
    });

    expect(await screen.findByText('Error loading NAT gateways')).toBeInTheDocument();
    expect(screen.getByText('NAT gateways unavailable')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled();
  });

  it('submits the NAT gateway name, virtual network id, and selected External IP', async () => {
    const onClose = vi.fn();
    let createRequest: NATGatewaysCreateRequest | undefined;
    const { user } = renderModal({
      onClose,
      transportOverrides: {
        onNatGatewayCreate: (req) => {
          createRequest = req;
          return create(NATGatewaysCreateResponseSchema, {
            object: { ...req.object, id: 'nat-created' } as NATGateway,
          });
        },
      },
    });

    await user.type(screen.getByLabelText(/^Name/), 'nat-egress');
    await waitFor(() => {
      expect(screen.getByLabelText(/^External IP/)).toHaveTextContent('eip-free · 203.0.113.10');
    });
    await user.click(screen.getByRole('button', { name: 'Next' }));
    expect(screen.getByRole('button', { name: 'Create' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => {
      expect(createRequest?.object?.metadata?.name).toBe('nat-egress');
    });
    expect(createRequest?.object?.spec?.virtualNetwork?.id).toBe('vn-1');
    expect(createRequest?.object?.spec?.externalIp?.id).toBe('eip-free');
    expect(createRequest?.object?.id).toBeFalsy();
    expect(createRequest?.object?.status).toBeUndefined();
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it('keeps the modal open when create fails', async () => {
    const onClose = vi.fn();
    const { user } = renderModal({
      onClose,
      transportOverrides: {
        onNatGatewayCreate: () => {
          throw new ConnectError('external IP already in use', Code.FailedPrecondition);
        },
      },
    });

    await user.type(screen.getByLabelText(/^Name/), 'nat-egress');
    await waitFor(() => {
      expect(screen.getByLabelText(/^External IP/)).toHaveTextContent('eip-free · 203.0.113.10');
    });
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Create' }));

    expect(await screen.findByText('Failed to create NAT gateway attachment')).toBeInTheDocument();
    expect(screen.getByText('external IP already in use')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'NAT gateway attachment' })).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });

  it('does not create a NAT gateway when Cancel is clicked', async () => {
    const onClose = vi.fn();
    const onNatGatewayCreate = vi.fn();
    const { user } = renderModal({
      onClose,
      transportOverrides: { onNatGatewayCreate },
    });

    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(onClose).toHaveBeenCalled();
    expect(onNatGatewayCreate).not.toHaveBeenCalled();
  });
});
