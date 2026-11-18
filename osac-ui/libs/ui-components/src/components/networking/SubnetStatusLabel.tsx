import { SubnetState } from '@osac/types';

import StatusWithMessage from './StatusWithMessage';
import { ResourceStatusLabel, type StatusKind } from '../Resource/ResourceStatusLabel';

interface SubnetStatusLabelProps {
  state?: SubnetState;
  message?: string;
}

const SUBNET_STATUS_MAP: Record<SubnetState, { status: StatusKind; text: string }> = {
  [SubnetState.UNSPECIFIED]: { status: 'unspecified', text: 'Unknown' },
  [SubnetState.PENDING]: { status: 'progressing', text: 'Provisioning' },
  [SubnetState.READY]: { status: 'ready', text: 'Ready' },
  [SubnetState.FAILED]: { status: 'failed', text: 'Failed' },
  [SubnetState.DELETING]: { status: 'progressing', text: 'Deleting' },
};

const resolveSubnetStatus = (state?: SubnetState): { status: StatusKind; text: string } => {
  if (state !== undefined && state in SUBNET_STATUS_MAP) {
    return SUBNET_STATUS_MAP[state];
  }
  return SUBNET_STATUS_MAP[SubnetState.UNSPECIFIED];
};

export const SubnetStatusLabel = ({ state, message }: SubnetStatusLabelProps) => {
  const { status, text } = resolveSubnetStatus(state);

  return (
    <StatusWithMessage message={message}>
      <ResourceStatusLabel status={status} text={text} />
    </StatusWithMessage>
  );
};
