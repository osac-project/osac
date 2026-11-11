import { VirtualNetworkState } from '@osac/types';

import { useTranslation } from '../../hooks/useTranslation';
import { ResourceStatusLabel, type StatusKind } from '../Resource/ResourceStatusLabel';

interface VirtualNetworkStatusLabelProps {
  state?: VirtualNetworkState;
}

const STATUS_KIND: Record<VirtualNetworkState, StatusKind> = {
  [VirtualNetworkState.UNSPECIFIED]: 'unspecified',
  [VirtualNetworkState.PENDING]: 'progressing',
  [VirtualNetworkState.READY]: 'ready',
  [VirtualNetworkState.FAILED]: 'failed',
  [VirtualNetworkState.DELETING]: 'progressing',
};

export const VirtualNetworkStatusLabel = ({ state }: VirtualNetworkStatusLabelProps) => {
  const { t } = useTranslation();
  const text: Record<VirtualNetworkState, string> = {
    [VirtualNetworkState.UNSPECIFIED]: t('Unknown'),
    [VirtualNetworkState.PENDING]: t('Provisioning'),
    [VirtualNetworkState.READY]: t('Ready'),
    [VirtualNetworkState.FAILED]: t('Failed'),
    [VirtualNetworkState.DELETING]: t('Deleting'),
  };
  const resolved =
    state !== undefined && state in STATUS_KIND ? state : VirtualNetworkState.UNSPECIFIED;

  return (
    <ResourceStatusLabel status={STATUS_KIND[resolved]} text={text[resolved]} noIcon={false} />
  );
};
