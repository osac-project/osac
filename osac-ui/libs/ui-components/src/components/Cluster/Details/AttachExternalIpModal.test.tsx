import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { ExternalIP, ExternalIPAttachmentsCreateRequest } from '@osac/types';
import {
  ExternalIPAttachmentEndpoint,
  ExternalIPAttachmentsCreateResponseSchema,
  ExternalIPState,
} from '@osac/types';

import AttachExternalIpModal from './AttachExternalIpModal';
import type { MockTransportOverrides } from '../../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const eligibleIp = {
  id: 'eip-1',
  metadata: { name: 'edge-ip' },
  status: {
    state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED,
    attached: false,
    address: '203.0.113.10',
  },
} as ExternalIP;

const renderModal = ({
  endpoint = ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
  onClose = vi.fn(),
  transportOverrides,
}: {
  endpoint?: ExternalIPAttachmentEndpoint;
  onClose?: () => void;
  transportOverrides?: MockTransportOverrides;
} = {}) =>
  renderWithProviders(
    <AttachExternalIpModal clusterId="cluster-1" endpoint={endpoint} onClose={onClose} />,
    { apiFixtures: { externalIps: [eligibleIp] }, transportOverrides },
  );

describe('AttachExternalIpModal', () => {
  it('submits a cluster endpoint attachment without exposing a name field', async () => {
    const onClose = vi.fn();
    let createRequest: ExternalIPAttachmentsCreateRequest | undefined;
    const { user } = renderModal({
      endpoint: ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
      onClose,
      transportOverrides: {
        onExternalIpAttachmentCreate: (request) => {
          createRequest = request;
          return create(ExternalIPAttachmentsCreateResponseSchema, {
            object: { id: 'attachment-1', spec: request.object?.spec },
          });
        },
      },
    });

    await waitFor(() => {
      expect(screen.getByLabelText(/^External IP/)).toHaveTextContent('edge-ip');
    });
    await user.click(screen.getByRole('button', { name: /^Attach$/ }));

    await waitFor(() => {
      expect(createRequest?.object?.spec?.externalIp?.id).toBe('eip-1');
    });
    expect(createRequest?.object?.spec?.target.case).toBe('cluster');
    expect(createRequest?.object?.spec?.target.value?.id).toBe('cluster-1');
    expect(createRequest?.object?.spec?.targetEndpoint).toBe(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
    );
    expect(createRequest?.object?.metadata?.name).toMatch(/^eipa-/);
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });
});
