import type { TFunction } from 'i18next';

import { type Volume, VolumeState } from '@osac/types';
import { type CelFilter, cel } from '@osac/ui-components/api/cel';
import type { StatusKind } from '@osac/ui-components/components/Resource/ResourceStatusLabel';

export const STATE_FILTER_VALUES = [
  'creating',
  'available',
  'failed',
  'deleting',
  'deleted',
] as const;

export type VolumeStateFilterValue = (typeof STATE_FILTER_VALUES)[number];

export const STATE_FILTER_PARAM = 'state';

export const isVolumeStateFilterValue = (value: string): value is VolumeStateFilterValue =>
  STATE_FILTER_VALUES.includes(value as VolumeStateFilterValue);

export const VOLUME_STATE_FILTER_TO_ENUM: Record<VolumeStateFilterValue, VolumeState> = {
  creating: VolumeState.CREATING,
  available: VolumeState.AVAILABLE,
  failed: VolumeState.FAILED,
  deleting: VolumeState.DELETING,
  deleted: VolumeState.DELETED,
};

export const getVolumeStatusMap = (
  t: TFunction,
): Record<VolumeState, { status: StatusKind; text: string }> => ({
  [VolumeState.UNSPECIFIED]: { status: 'unspecified', text: t('Unspecified') },
  [VolumeState.CREATING]: { status: 'progressing', text: t('Creating') },
  [VolumeState.AVAILABLE]: { status: 'ready', text: t('Available') },
  [VolumeState.FAILED]: { status: 'failed', text: t('Failed') },
  [VolumeState.DELETING]: { status: 'unspecified', text: t('Deleting') },
  [VolumeState.DELETED]: { status: 'unspecified', text: t('Deleted') },
});

export const formatSizeGib = (sizeGib: bigint | undefined): string =>
  sizeGib === undefined ? '—' : `${sizeGib} GiB`;

export const buildVolumeListFilter = (
  projectFilter: CelFilter<Volume> | undefined,
  stateFilters: VolumeState[],
  nameSearch: string,
): CelFilter<Volume> | undefined =>
  cel<Volume>((b) =>
    b.and(
      projectFilter,
      stateFilters.length > 0 ? b.field('status.state').isIn(stateFilters) : undefined,
      nameSearch ? b.field('metadata.name').contains(nameSearch) : undefined,
    ),
  );
