import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { ExternalIP } from '@osac/types';
import { ExternalIPState } from '@osac/types';

import AttachExternalIpModal, { EXTERNAL_IP_PICKER_LIMIT } from './AttachExternalIpModal';
import type { MockTransportOverrides } from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

const eligibleIp = {
  id: 'eip-1',
  metadata: { name: 'edge-ip' },
  status: {
    state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED,
    attached: false,
    address: '203.0.113.10',
  },
} as ExternalIP;

const attachedIp = {
  id: 'eip-attached',
  metadata: { name: 'in-use-ip' },
  status: {
    state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED,
    attached: true,
    address: '203.0.113.11',
  },
} as ExternalIP;

const renderModal = ({
  onAttach = vi.fn().mockResolvedValue(undefined),
  onClose = vi.fn(),
  externalIps = [eligibleIp],
  transportOverrides,
}: {
  onAttach?: (externalIpId: string) => Promise<unknown>;
  onClose?: () => void;
  externalIps?: ExternalIP[];
  transportOverrides?: MockTransportOverrides;
} = {}) =>
  renderWithProviders(
    <AttachExternalIpModal
      title="Attach external IP"
      emptyDescription="Create an external IP first, then attach it to this endpoint."
      onAttach={onAttach}
      onClose={onClose}
    />,
    { apiFixtures: { externalIps }, transportOverrides },
  );

describe('AttachExternalIpModal', () => {
  it('submits the selected unattached IP and closes after success', async () => {
    const onAttach = vi.fn().mockResolvedValue(undefined);
    const onClose = vi.fn();
    const { user } = renderModal({ onAttach, onClose });

    await waitFor(() => {
      expect(screen.getByLabelText(/^External IP/)).toHaveTextContent('edge-ip');
    });
    expect(screen.queryByLabelText(/^Name/)).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: /^Attach$/ }));

    await waitFor(() => expect(onAttach).toHaveBeenCalledWith('eip-1'));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it('lists only allocated unattached external IPs and bounds the request', async () => {
    let requestedLimit: number | undefined;
    const { user } = renderModal({
      externalIps: [eligibleIp, attachedIp],
      transportOverrides: {
        onExternalIpList: (request) => {
          requestedLimit = request.limit;
        },
      },
    });

    await waitFor(() => expect(requestedLimit).toBe(EXTERNAL_IP_PICKER_LIMIT));
    await user.click(await screen.findByLabelText(/^External IP/));

    expect(screen.getByRole('option', { name: 'edge-ip' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'in-use-ip' })).not.toBeInTheDocument();
  });

  it('shows an empty state and disables attach when no IP is available', async () => {
    renderModal({ externalIps: [attachedIp] });

    expect(await screen.findByText('No unattached external IPs available')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^Attach$/ })).toBeDisabled();
  });

  it('shows a loading error when external IPs cannot be retrieved', async () => {
    renderModal({
      transportOverrides: {
        onExternalIpList: () => {
          throw new ConnectError('ips unavailable', Code.Unavailable);
        },
      },
    });

    expect(await screen.findByText('Error loading external IPs')).toBeInTheDocument();
    expect(screen.getByText('ips unavailable')).toBeInTheDocument();
  });

  it('keeps the modal open and allows retry after attach failure', async () => {
    const onAttach = vi
      .fn()
      .mockRejectedValue(new ConnectError('already attached', Code.AlreadyExists));
    const onClose = vi.fn();
    const { user } = renderModal({ onAttach, onClose });

    await waitFor(() => {
      expect(screen.getByLabelText(/^External IP/)).toHaveTextContent('edge-ip');
    });
    await user.click(screen.getByRole('button', { name: /^Attach$/ }));

    expect(await screen.findByText('already attached')).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^Attach$/ })).toBeEnabled();
  });
});
