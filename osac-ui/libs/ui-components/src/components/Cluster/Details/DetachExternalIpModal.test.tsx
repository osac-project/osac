import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { ExternalIPAttachment, ExternalIPAttachmentsDeleteRequest } from '@osac/types';
import {
  ExternalIPAttachmentEndpoint,
  ExternalIPAttachmentsDeleteResponseSchema,
} from '@osac/types';

import DetachExternalIpModal from './DetachExternalIpModal';
import type { MockTransportOverrides } from '../../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const attachment = {
  id: 'attachment-1',
  spec: {
    externalIp: { id: 'eip-1' },
    target: { case: 'cluster', value: { id: 'cluster-1' } },
    targetEndpoint: ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
  },
  status: { externalIpAddress: '203.0.113.10' },
} as unknown as ExternalIPAttachment;

const renderModal = ({
  attachment: attachmentOverride = attachment,
  onClose = vi.fn(),
  transportOverrides,
}: {
  attachment?: ExternalIPAttachment;
  onClose?: () => void;
  transportOverrides?: MockTransportOverrides;
} = {}) =>
  renderWithProviders(<DetachExternalIpModal attachment={attachmentOverride} onClose={onClose} />, {
    apiFixtures: { externalIpAttachments: [attachmentOverride] },
    transportOverrides,
  });

describe('DetachExternalIpModal', () => {
  it('confirms deletion of the endpoint attachment by id', async () => {
    const onClose = vi.fn();
    let deleteRequest: ExternalIPAttachmentsDeleteRequest | undefined;
    const { user } = renderModal({
      onClose,
      transportOverrides: {
        onExternalIpAttachmentDelete: (request) => {
          deleteRequest = request;
          return create(ExternalIPAttachmentsDeleteResponseSchema);
        },
      },
    });

    expect(screen.getByRole('heading', { name: /Delete 203\.0\.113\.10\?/ })).toBeInTheDocument();
    expect(
      screen.getByText('This detaches the external IP from the API endpoint.'),
    ).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(deleteRequest?.id).toBe('attachment-1'));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it('shows a delete error, keeps the dialog open, and allows retry', async () => {
    const onClose = vi.fn();
    let attempt = 0;
    const { user } = renderModal({
      onClose,
      transportOverrides: {
        onExternalIpAttachmentDelete: () => {
          attempt += 1;
          if (attempt === 1) {
            throw new ConnectError('detach failed', Code.FailedPrecondition);
          }
          return create(ExternalIPAttachmentsDeleteResponseSchema);
        },
      },
    });

    await user.click(screen.getByRole('button', { name: 'Delete' }));

    expect(await screen.findByText('detach failed')).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Delete' })).toBeEnabled();

    await user.click(screen.getByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(attempt).toBe(2));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it('allows detaching when the attachment endpoint is unspecified', () => {
    const unspecifiedAttachment = {
      ...attachment,
      spec: {
        ...attachment.spec,
        targetEndpoint: ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_UNSPECIFIED,
      },
    } as unknown as ExternalIPAttachment;

    renderModal({ attachment: unspecifiedAttachment });

    expect(screen.getByText('This detaches the external IP from the cluster.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete' })).toBeEnabled();
  });
});
