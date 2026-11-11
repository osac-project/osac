import { ExternalIPAttachmentState } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { ResourceStatusLabel, type StatusKind } from '../../Resource/ResourceStatusLabel';

interface ExternalIpAttachmentStatusLabelProps {
  state?: ExternalIPAttachmentState;
}

const STATUS_KIND: Record<ExternalIPAttachmentState, StatusKind> = {
  [ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_UNSPECIFIED]: 'unspecified',
  [ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_PENDING]: 'progressing',
  [ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_READY]: 'ready',
  [ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_FAILED]: 'failed',
  [ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_DELETING]: 'progressing',
};

const ExternalIpAttachmentStatusLabel = ({ state }: ExternalIpAttachmentStatusLabelProps) => {
  const { t } = useTranslation();
  const text: Record<ExternalIPAttachmentState, string> = {
    [ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_UNSPECIFIED]: t('Unknown'),
    [ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_PENDING]: t('Attaching'),
    [ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_READY]: t('Attached'),
    [ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_FAILED]: t('Failed'),
    [ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_DELETING]: t('Detaching'),
  };
  const resolved =
    state !== undefined && state in STATUS_KIND
      ? state
      : ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_UNSPECIFIED;

  return (
    <ResourceStatusLabel status={STATUS_KIND[resolved]} text={text[resolved]} noIcon={false} />
  );
};

export default ExternalIpAttachmentStatusLabel;
