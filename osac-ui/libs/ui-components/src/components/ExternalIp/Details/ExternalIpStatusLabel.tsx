import type { TFunction } from 'i18next';

import { ExternalIPState } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { ResourceStatusLabel, type StatusLabelProps } from '../../Resource/ResourceStatusLabel';

interface ExternalIpStatusLabelProps {
  state?: ExternalIPState;
}

/** The API state of an ExternalIP; the Used by column says what uses it. */
const externalIpStatusMap = (t: TFunction): Record<ExternalIPState, StatusLabelProps> => ({
  [ExternalIPState.EXTERNAL_IP_STATE_UNSPECIFIED]: {
    status: 'unspecified',
    text: t('Unknown'),
  },
  [ExternalIPState.EXTERNAL_IP_STATE_PENDING]: {
    status: 'progressing',
    text: t('Provisioning'),
  },
  [ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED]: {
    status: 'ready',
    text: t('Allocated'),
  },
  [ExternalIPState.EXTERNAL_IP_STATE_FAILED]: {
    status: 'failed',
    text: t('Failed'),
  },
  [ExternalIPState.EXTERNAL_IP_STATE_DELETING]: {
    status: 'progressing',
    text: t('Deleting'),
  },
});

const ExternalIpStatusLabel = ({ state }: ExternalIpStatusLabelProps) => {
  const { t } = useTranslation();
  const statusMap = externalIpStatusMap(t);
  const status =
    state !== undefined && state in statusMap
      ? statusMap[state]
      : statusMap[ExternalIPState.EXTERNAL_IP_STATE_UNSPECIFIED];

  return <ResourceStatusLabel {...status} noIcon={false} />;
};

export default ExternalIpStatusLabel;
