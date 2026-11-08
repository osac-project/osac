import { NetworkClassState } from '@osac/types/private';

import { useTranslation } from '../../hooks/useTranslation';
import { ResourceStatusLabel, type StatusKind } from '../Resource/ResourceStatusLabel';

interface NetworkClassStatusLabelProps {
  state?: NetworkClassState;
}

const STATUS_KIND: Record<NetworkClassState, StatusKind> = {
  [NetworkClassState.UNSPECIFIED]: 'unspecified',
  [NetworkClassState.PENDING]: 'progressing',
  [NetworkClassState.READY]: 'ready',
  [NetworkClassState.FAILED]: 'failed',
};

export const NetworkClassStatusLabel = ({ state }: NetworkClassStatusLabelProps) => {
  const { t } = useTranslation();
  const resolved =
    state !== undefined && state in STATUS_KIND ? state : NetworkClassState.UNSPECIFIED;
  const text: Record<NetworkClassState, string> = {
    [NetworkClassState.UNSPECIFIED]: t('Unknown'),
    [NetworkClassState.PENDING]: t('Pending'),
    [NetworkClassState.READY]: t('Ready'),
    [NetworkClassState.FAILED]: t('Failed'),
  };

  return (
    <ResourceStatusLabel status={STATUS_KIND[resolved]} text={text[resolved]} noIcon={false} />
  );
};
