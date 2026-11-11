import { SecurityGroupState } from '@osac/types';

import { SECURITY_GROUP_UPDATING_MESSAGE } from './securityGroupRuleUtils';
import { useTranslation } from '../../hooks/useTranslation';
import { ResourceStatusLabel, type StatusKind } from '../Resource/ResourceStatusLabel';

interface SecurityGroupStatusLabelProps {
  state?: SecurityGroupState;
  /** The API status message; a READY group with the message `Updating` shows Updating. */
  message?: string;
}

const STATUS_KIND: Record<SecurityGroupState, StatusKind> = {
  [SecurityGroupState.UNSPECIFIED]: 'unspecified',
  [SecurityGroupState.PENDING]: 'progressing',
  [SecurityGroupState.READY]: 'ready',
  [SecurityGroupState.FAILED]: 'failed',
  [SecurityGroupState.DELETING]: 'progressing',
};

export const SecurityGroupStatusLabel = ({ state, message }: SecurityGroupStatusLabelProps) => {
  const { t } = useTranslation();

  if (state === SecurityGroupState.READY && message === SECURITY_GROUP_UPDATING_MESSAGE) {
    return <ResourceStatusLabel status="progressing" text={t('Updating')} noIcon={false} />;
  }

  const text: Record<SecurityGroupState, string> = {
    [SecurityGroupState.UNSPECIFIED]: t('Unknown'),
    [SecurityGroupState.PENDING]: t('Provisioning'),
    [SecurityGroupState.READY]: t('Ready'),
    [SecurityGroupState.FAILED]: t('Failed'),
    [SecurityGroupState.DELETING]: t('Deleting'),
  };
  const resolved =
    state !== undefined && state in STATUS_KIND ? state : SecurityGroupState.UNSPECIFIED;

  return (
    <ResourceStatusLabel status={STATUS_KIND[resolved]} text={text[resolved]} noIcon={false} />
  );
};
