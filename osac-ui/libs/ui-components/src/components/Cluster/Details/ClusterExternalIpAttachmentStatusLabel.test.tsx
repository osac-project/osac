import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ExternalIPAttachmentState } from '@osac/types';
import type { ExternalIPAttachment } from '@osac/types';

import ClusterExternalIpAttachmentStatusLabel from './ClusterExternalIpAttachmentStatusLabel';

const makeAttachment = (state: ExternalIPAttachmentState): ExternalIPAttachment =>
  ({ status: { state } }) as ExternalIPAttachment;

const getLabel = (text: string) => screen.getByText(text).closest('.pf-v6-c-label');

describe('ClusterExternalIpAttachmentStatusLabel', () => {
  it.each([
    [
      ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_UNSPECIFIED,
      'External IP attachment pending',
    ],
    [ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_PENDING, 'Attaching'],
    [ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_READY, 'Attached'],
    [ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_FAILED, 'Failed'],
    [ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_DELETING, 'Detaching'],
  ] as const)('maps %s to %s', (state, text) => {
    render(
      <ClusterExternalIpAttachmentStatusLabel
        attachment={makeAttachment(state)}
        autoAttachmentRequested={false}
      />,
    );

    expect(getLabel(text)).toBeInTheDocument();
  });

  it('shows auto-attachment progress when no attachment exists yet', () => {
    render(
      <ClusterExternalIpAttachmentStatusLabel autoAttachmentRequested attachment={undefined} />,
    );

    expect(getLabel('Auto attaching in progress')).toBeInTheDocument();
  });

  it('shows one auto-attached label for a ready attachment', () => {
    render(
      <ClusterExternalIpAttachmentStatusLabel
        attachment={makeAttachment(ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_READY)}
        autoAttachmentRequested
      />,
    );

    expect(getLabel('Auto attached')).toBeInTheDocument();
    expect(screen.queryByText('Attached')).not.toBeInTheDocument();
  });

  it('does not render a status when nothing is attached and auto-attachment is disabled', () => {
    const { container } = render(
      <ClusterExternalIpAttachmentStatusLabel
        attachment={undefined}
        autoAttachmentRequested={false}
      />,
    );

    expect(container).toBeEmptyDOMElement();
  });
});
