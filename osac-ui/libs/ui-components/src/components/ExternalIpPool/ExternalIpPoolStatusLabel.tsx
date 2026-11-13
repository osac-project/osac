import type { TFunction } from 'i18next';

import { ExternalIPPoolState } from '@osac/types/private';

import { getExternalIpPoolStateLabels } from './utils';
import { useTranslation } from '../../hooks/useTranslation';
import { ResourceStatusLabel, type StatusLabelProps } from '../Resource/ResourceStatusLabel';

interface ExternalIpPoolStatusLabelProps {
  state?: ExternalIPPoolState;
}

const externalIpPoolStatusMap = (t: TFunction): Record<ExternalIPPoolState, StatusLabelProps> => {
  const stateLabels = getExternalIpPoolStateLabels(t);

  return {
    [ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_UNSPECIFIED]: {
      status: 'unspecified',
      text: stateLabels[ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_UNSPECIFIED],
    },
    [ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_PENDING]: {
      status: 'progressing',
      text: stateLabels[ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_PENDING],
    },
    [ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_READY]: {
      status: 'ready',
      text: stateLabels[ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_READY],
    },
    [ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_FAILED]: {
      status: 'failed',
      text: stateLabels[ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_FAILED],
    },
    [ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_DELETING]: {
      status: 'progressing',
      text: stateLabels[ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_DELETING],
    },
  };
};

const ExternalIpPoolStatusLabel = ({ state }: ExternalIpPoolStatusLabelProps) => {
  const { t } = useTranslation();

  const statusMap = externalIpPoolStatusMap(t);
  const status =
    state !== undefined && state in statusMap
      ? statusMap[state]
      : statusMap[ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_UNSPECIFIED];

  return <ResourceStatusLabel {...status} />;
};

export default ExternalIpPoolStatusLabel;
