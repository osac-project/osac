import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { ComputeInstance, ExternalIP, ExternalIPAttachmentsCreateRequest } from '@osac/types';
import { ExternalIPAttachmentsCreateResponseSchema, ExternalIPState } from '@osac/types';

import AttachExternalIpModal from './AttachExternalIpModal';
import type { MockTransportOverrides } from '../../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const ATTACH_BUTTON_NAME = /Attach/i;

const vm = { id: 'vm-1', metadata: { name: 'test-vm' } } as ComputeInstance;

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
  onClose = vi.fn(),
  onSuccess = vi.fn(),
  externalIps = [eligibleIp],
  transportOverrides,
}: {
  onClose?: () => void;
  onSuccess?: () => void;
  externalIps?: ExternalIP[];
  transportOverrides?: MockTransportOverrides;
} = {}) =>
  renderWithProviders(<AttachExternalIpModal vm={vm} onClose={onClose} onSuccess={onSuccess} />, {
    apiFixtures: { externalIps },
    transportOverrides,
  });

describe('AttachExternalIpModal', () => {
  it('submits an attachment for the auto-selected unattached IP', async () => {
    const onSuccess = vi.fn();
    let createRequest: ExternalIPAttachmentsCreateRequest | undefined;
    const { user } = renderModal({
      onSuccess,
      transportOverrides: {
        onExternalIpAttachmentCreate: (req) => {
          createRequest = req;
          return create(ExternalIPAttachmentsCreateResponseSchema, {
            object: { id: 'attachment-1', spec: req.object?.spec },
          });
        },
      },
    });

    await waitFor(() => {
      expect(screen.getByLabelText(/^External IP/)).toHaveTextContent('edge-ip');
    });
    await user.click(screen.getByRole('button', { name: ATTACH_BUTTON_NAME }));

    await waitFor(() => {
      expect(createRequest?.object?.spec?.externalIp?.id).toBe('eip-1');
    });
    expect(createRequest?.object?.metadata?.name).toMatch(
      /^eipa-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/,
    );
    expect(createRequest?.object?.spec?.target.case).toBe('computeInstance');
    expect(createRequest?.object?.spec?.target.value?.id).toBe('vm-1');
    await waitFor(() => {
      expect(onSuccess).toHaveBeenCalled();
    });
  });

  it('does not list attached external IPs', async () => {
    renderModal({ externalIps: [eligibleIp, attachedIp] });

    await waitFor(() => {
      expect(screen.getByLabelText(/^External IP/)).not.toBeDisabled();
    });
    expect(screen.getByLabelText(/^External IP/)).toHaveTextContent('edge-ip');
    expect(screen.queryByText('in-use-ip')).not.toBeInTheDocument();
  });

  it('shows a warning and disables attach when no unattached IPs are available', async () => {
    renderModal({ externalIps: [attachedIp] });

    expect(await screen.findByText('No unattached external IPs available')).toBeInTheDocument();
    expect(
      screen.getByText('Create an external IP first, then attach it to this virtual machine.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: ATTACH_BUTTON_NAME })).toBeDisabled();
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

  it('renders an inline error and does not close on attach failure', async () => {
    const onSuccess = vi.fn();
    const { user } = renderModal({
      onSuccess,
      transportOverrides: {
        onExternalIpAttachmentCreate: () => {
          throw new ConnectError('already attached', Code.AlreadyExists);
        },
      },
    });

    await waitFor(() => {
      expect(screen.getByLabelText(/^External IP/)).toHaveTextContent('edge-ip');
    });
    await user.click(screen.getByRole('button', { name: ATTACH_BUTTON_NAME }));

    expect(await screen.findByText('already attached')).toBeInTheDocument();
    expect(onSuccess).not.toHaveBeenCalled();
  });
});
