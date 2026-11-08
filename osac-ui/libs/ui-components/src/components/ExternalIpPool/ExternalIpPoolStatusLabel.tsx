import type { TFunction } from 'i18next';

import { ExternalIPPoolState } from '@osac/types/private';

import { useTranslation } from '../../hooks/useTranslation';
import { ResourceStatusLabel, type StatusLabelProps } from '../Resource/ResourceStatusLabel';

interface ExternalIpPoolStatusLabelProps {
  state?: ExternalIPPoolState;
}

const externalIpPoolStatusMap = (t: TFunction): Record<ExternalIPPoolState, StatusLabelProps> => ({
  [ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_UNSPECIFIED]: {
    status: 'unspecified',
    text: t('Unknown'),
  },
  [ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_PENDING]: {
    status: 'progressing',
    text: t('Provisioning'),
  },
  [ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_READY]: {
    status: 'ready',
    text: t('Ready'),
  },
  [ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_FAILED]: {
    status: 'failed',
    text: t('Failed'),
  },
  [ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_DELETING]: {
    status: 'progressing',
    text: t('Deleting'),
  },
});

const ExternalIpPoolStatusLabel = ({ state }: ExternalIpPoolStatusLabelProps) => {
  const { t } = useTranslation();

  const statusMap = externalIpPoolStatusMap(t);
  const status =
    state !== undefined && state in statusMap
      ? statusMap[state]
      : statusMap[ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_UNSPECIFIED];

  return <ResourceStatusLabel {...status} noIcon={false} />;
};

export default ExternalIpPoolStatusLabel;
