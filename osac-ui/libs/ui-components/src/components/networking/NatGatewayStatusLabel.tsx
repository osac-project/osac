import type { TFunction } from 'i18next';

import { NATGatewayState } from '@osac/types';

import { useTranslation } from '../../hooks/useTranslation';
import { ResourceStatusLabel, type StatusKind } from '../Resource/ResourceStatusLabel';

interface NatGatewayStatusLabelProps {
  state?: NATGatewayState;
}

const NAT_GATEWAY_STATUS_MAP: Record<NATGatewayState, StatusKind> = {
  [NATGatewayState.NAT_GATEWAY_STATE_UNSPECIFIED]: 'unspecified',
  [NATGatewayState.NAT_GATEWAY_STATE_PENDING]: 'progressing',
  [NATGatewayState.NAT_GATEWAY_STATE_READY]: 'ready',
  [NATGatewayState.NAT_GATEWAY_STATE_FAILED]: 'failed',
  [NATGatewayState.NAT_GATEWAY_STATE_DELETING]: 'progressing',
};

const resolveNatGatewayStatus = (
  t: TFunction,
  state?: NATGatewayState,
): { status: StatusKind; text: string } => {
  switch (state) {
    case NATGatewayState.NAT_GATEWAY_STATE_PENDING:
      return { status: NAT_GATEWAY_STATUS_MAP[state], text: t('Provisioning') };
    case NATGatewayState.NAT_GATEWAY_STATE_READY:
      return { status: NAT_GATEWAY_STATUS_MAP[state], text: t('Ready') };
    case NATGatewayState.NAT_GATEWAY_STATE_FAILED:
      return { status: NAT_GATEWAY_STATUS_MAP[state], text: t('Failed') };
    case NATGatewayState.NAT_GATEWAY_STATE_DELETING:
      return { status: NAT_GATEWAY_STATUS_MAP[state], text: t('Deleting') };
    case NATGatewayState.NAT_GATEWAY_STATE_UNSPECIFIED:
    default:
      return {
        status: NAT_GATEWAY_STATUS_MAP[NATGatewayState.NAT_GATEWAY_STATE_UNSPECIFIED],
        text: t('Unknown'),
      };
  }
};

export const NatGatewayStatusLabel = ({ state }: NatGatewayStatusLabelProps) => {
  const { t } = useTranslation();
  const { status, text } = resolveNatGatewayStatus(t, state);

  return <ResourceStatusLabel status={status} text={text} noIcon={false} />;
};
