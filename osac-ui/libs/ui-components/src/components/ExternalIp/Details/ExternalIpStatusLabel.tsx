import type { TFunction } from 'i18next';

import { ExternalIPState } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { ResourceStatusLabel, type StatusLabelProps } from '../../Resource/ResourceStatusLabel';

interface ExternalIpStatusLabelProps {
  state?: ExternalIPState;
  attached?: boolean;
}

const allocatedStatus = (t: TFunction, attached?: boolean): StatusLabelProps =>
  attached
    ? { status: 'ready', text: t('In use'), color: 'blue' }
    : { status: 'ready', text: t('Allocated'), color: 'green' };

const externalIpStatusMap = (
  t: TFunction,
  attached?: boolean,
): Record<ExternalIPState, StatusLabelProps> => ({
  [ExternalIPState.EXTERNAL_IP_STATE_UNSPECIFIED]: {
    status: 'unspecified',
    text: t('Unknown'),
  },
  [ExternalIPState.EXTERNAL_IP_STATE_PENDING]: {
    status: 'progressing',
    text: t('Provisioning'),
  },
  [ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED]: allocatedStatus(t, attached),
  [ExternalIPState.EXTERNAL_IP_STATE_FAILED]: {
    status: 'failed',
    text: t('Failed'),
  },
  [ExternalIPState.EXTERNAL_IP_STATE_DELETING]: {
    status: 'progressing',
    text: t('Deleting'),
  },
});

const ExternalIpStatusLabel = ({ state, attached }: ExternalIpStatusLabelProps) => {
  const { t } = useTranslation();
  const statusMap = externalIpStatusMap(t, attached);
  const status =
    state !== undefined && state in statusMap
      ? statusMap[state]
      : statusMap[ExternalIPState.EXTERNAL_IP_STATE_UNSPECIFIED];

  return <ResourceStatusLabel {...status} />;
};

export default ExternalIpStatusLabel;
