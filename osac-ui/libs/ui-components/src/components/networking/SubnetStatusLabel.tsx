import { SubnetState } from '@osac/types';

import { ResourceStatusLabel, type StatusKind } from '../Resource/ResourceStatusLabel';

interface SubnetStatusLabelProps {
  state?: SubnetState;
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

export const SubnetStatusLabel = ({ state }: SubnetStatusLabelProps) => {
  const { status, text } = resolveSubnetStatus(state);

  return <ResourceStatusLabel status={status} text={text} />;
};
