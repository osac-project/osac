import { SubnetState } from '@osac/types';

import { useTranslation } from '../../hooks/useTranslation';
import { ResourceStatusLabel, type StatusKind } from '../Resource/ResourceStatusLabel';

interface SubnetStatusLabelProps {
  state?: SubnetState;
}

const STATUS_KIND: Record<SubnetState, StatusKind> = {
  [SubnetState.UNSPECIFIED]: 'unspecified',
  [SubnetState.PENDING]: 'progressing',
  [SubnetState.READY]: 'ready',
  [SubnetState.FAILED]: 'failed',
  [SubnetState.DELETING]: 'progressing',
};

export const SubnetStatusLabel = ({ state }: SubnetStatusLabelProps) => {
  const { t } = useTranslation();
  const text: Record<SubnetState, string> = {
    [SubnetState.UNSPECIFIED]: t('Unknown'),
    [SubnetState.PENDING]: t('Provisioning'),
    [SubnetState.READY]: t('Ready'),
    [SubnetState.FAILED]: t('Failed'),
    [SubnetState.DELETING]: t('Deleting'),
  };
  const resolved = state !== undefined && state in STATUS_KIND ? state : SubnetState.UNSPECIFIED;

  return (
    <ResourceStatusLabel status={STATUS_KIND[resolved]} text={text[resolved]} noIcon={false} />
  );
};
