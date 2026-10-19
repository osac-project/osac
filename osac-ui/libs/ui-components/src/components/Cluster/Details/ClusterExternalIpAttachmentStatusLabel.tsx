import { Content, FlexItem } from '@patternfly/react-core';
import type { TFunction } from 'i18next';

import { ExternalIPAttachmentState } from '@osac/types';
import type { ExternalIPAttachment } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { ResourceStatusLabel, type StatusKind } from '../../Resource/ResourceStatusLabel';

type AttachmentStatus = {
  status: StatusKind;
  text: string;
};

const attachmentStatusMap = (
  t: TFunction,
): Record<ExternalIPAttachmentState, AttachmentStatus> => ({
  [ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_UNSPECIFIED]: {
    status: 'unspecified',
    text: t('External IP attachment pending'),
  },
  [ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_PENDING]: {
    status: 'progressing',
    text: t('Attaching'),
  },
  [ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_READY]: {
    status: 'ready',
    text: t('Attached'),
  },
  [ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_FAILED]: {
    status: 'failed',
    text: t('Failed'),
  },
  [ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_DELETING]: {
    status: 'progressing',
    text: t('Detaching'),
  },
});

const resolveAttachmentStatus = (
  t: TFunction,
  state: ExternalIPAttachmentState | undefined,
  autoAttachmentRequested: boolean,
): AttachmentStatus => {
  if (autoAttachmentRequested) {
    return state === ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_READY
      ? { status: 'ready', text: t('Auto attached') }
      : { status: 'progressing', text: t('Auto attaching in progress') };
  }

  const statusMap = attachmentStatusMap(t);
  return state !== undefined && state in statusMap
    ? statusMap[state]
    : statusMap[ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_UNSPECIFIED];
};

export interface ClusterExternalIpAttachmentStatusLabelProps {
  attachment?: ExternalIPAttachment;
  autoAttachmentRequested: boolean;
}

const ClusterExternalIpAttachmentStatusLabel = ({
  attachment,
  autoAttachmentRequested,
}: ClusterExternalIpAttachmentStatusLabelProps) => {
  const { t } = useTranslation();

  if (!attachment && !autoAttachmentRequested) {
    return null;
  }

  const { status, text } = resolveAttachmentStatus(
    t,
    attachment?.status?.state,
    autoAttachmentRequested,
  );

  return (
    <FlexItem>
      <Content component="p">
        <ResourceStatusLabel status={status} text={text} />
        {attachment?.status?.message ? ` ${attachment.status.message}` : null}
      </Content>
    </FlexItem>
  );
};

export default ClusterExternalIpAttachmentStatusLabel;
