import { VirtualNetworkState } from '@osac/types';

import StatusWithMessage from './StatusWithMessage';
import { ResourceStatusLabel, type StatusKind } from '../Resource/ResourceStatusLabel';

interface VirtualNetworkStatusLabelProps {
  state?: VirtualNetworkState;
  message?: string;
}

const VIRTUAL_NETWORK_STATUS_MAP: Record<
  VirtualNetworkState,
  { status: StatusKind; text: string }
> = {
  [VirtualNetworkState.UNSPECIFIED]: { status: 'unspecified', text: 'Unknown' },
  [VirtualNetworkState.PENDING]: { status: 'progressing', text: 'Provisioning' },
  [VirtualNetworkState.READY]: { status: 'ready', text: 'Ready' },
  [VirtualNetworkState.FAILED]: { status: 'failed', text: 'Failed' },
  [VirtualNetworkState.DELETING]: { status: 'progressing', text: 'Deleting' },
};

const resolveVirtualNetworkStatus = (
  state?: VirtualNetworkState,
): { status: StatusKind; text: string } => {
  if (state !== undefined && state in VIRTUAL_NETWORK_STATUS_MAP) {
    return VIRTUAL_NETWORK_STATUS_MAP[state];
  }
  return VIRTUAL_NETWORK_STATUS_MAP[VirtualNetworkState.UNSPECIFIED];
};

export const VirtualNetworkStatusLabel = ({ state, message }: VirtualNetworkStatusLabelProps) => {
  const { status, text } = resolveVirtualNetworkStatus(state);

  return (
    <StatusWithMessage message={message}>
      <ResourceStatusLabel status={status} text={text} />
    </StatusWithMessage>
  );
};
