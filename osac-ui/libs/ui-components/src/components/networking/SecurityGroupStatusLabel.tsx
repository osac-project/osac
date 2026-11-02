import { SecurityGroupState } from '@osac/types';

import { ResourceStatusLabel, type StatusKind } from '../Resource/ResourceStatusLabel';

interface SecurityGroupStatusLabelProps {
  state?: SecurityGroupState;
}

const SECURITY_GROUP_STATUS_MAP: Record<SecurityGroupState, { status: StatusKind; text: string }> =
  {
    [SecurityGroupState.UNSPECIFIED]: { status: 'unspecified', text: 'Unknown' },
    [SecurityGroupState.PENDING]: { status: 'progressing', text: 'Provisioning' },
    [SecurityGroupState.READY]: { status: 'ready', text: 'Ready' },
    [SecurityGroupState.FAILED]: { status: 'failed', text: 'Failed' },
    [SecurityGroupState.DELETING]: { status: 'progressing', text: 'Deleting' },
  };

const resolveSecurityGroupStatus = (
  state?: SecurityGroupState,
): { status: StatusKind; text: string } =>
  state !== undefined && state in SECURITY_GROUP_STATUS_MAP
    ? SECURITY_GROUP_STATUS_MAP[state]
    : SECURITY_GROUP_STATUS_MAP[SecurityGroupState.UNSPECIFIED];

export const SecurityGroupStatusLabel = ({ state }: SecurityGroupStatusLabelProps) => {
  const { status, text } = resolveSecurityGroupStatus(state);

  return <ResourceStatusLabel status={status} text={text} />;
};
