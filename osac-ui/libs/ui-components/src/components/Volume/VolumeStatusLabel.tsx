import type { TFunction } from 'i18next';

import { VolumeState } from '@osac/types';

import { useTranslation } from '../../hooks/useTranslation';
import { ResourceStatusLabel, type StatusKind } from '../Resource/ResourceStatusLabel';

interface VolumeStatusLabelProps {
  state?: VolumeState;
}

const getVolumeStatusMap = (
  t: TFunction,
): Record<VolumeState, { status: StatusKind; text: string }> => ({
  [VolumeState.UNSPECIFIED]: { status: 'unspecified', text: t('Unspecified') },
  [VolumeState.CREATING]: { status: 'progressing', text: t('Creating') },
  [VolumeState.AVAILABLE]: { status: 'ready', text: t('Available') },
  [VolumeState.FAILED]: { status: 'failed', text: t('Failed') },
  [VolumeState.DELETING]: { status: 'unspecified', text: t('Deleting') },
  [VolumeState.DELETED]: { status: 'unspecified', text: t('Deleted') },
});

const resolveVolumeStatus = (
  state: VolumeState | undefined,
  t: TFunction,
): { status: StatusKind; text: string } => {
  const map = getVolumeStatusMap(t);
  if (state !== undefined && state in map) {
    return map[state];
  }
  return map[VolumeState.UNSPECIFIED];
};

export const VolumeStatusLabel = ({ state }: VolumeStatusLabelProps) => {
  const { t } = useTranslation();
  const { status, text } = resolveVolumeStatus(state, t);

  return <ResourceStatusLabel status={status} text={text} />;
};
