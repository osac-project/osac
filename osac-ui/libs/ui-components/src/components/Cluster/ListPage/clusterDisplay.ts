import type { TFunction } from 'i18next';

import { ClusterState, Metadata } from '@osac/types';

export type ClusterStatusFilter = 'progressing' | 'ready' | 'failed' | 'deleting' | 'delete-failed';

export const CLUSTER_STATUS_FILTERS: readonly ClusterStatusFilter[] = [
  'progressing',
  'ready',
  'failed',
  'deleting',
  'delete-failed',
];

const CLUSTER_STATUS_FILTER_TO_STATE: Record<ClusterStatusFilter, ClusterState> = {
  progressing: ClusterState.PROGRESSING,
  ready: ClusterState.READY,
  failed: ClusterState.FAILED,
  deleting: ClusterState.DELETING,
  'delete-failed': ClusterState.DELETE_FAILED,
};

export const clusterStatusFilterToState = (filter: ClusterStatusFilter): ClusterState =>
  CLUSTER_STATUS_FILTER_TO_STATE[filter];

export const isClusterStatusFilter = (value: string): value is ClusterStatusFilter =>
  (CLUSTER_STATUS_FILTERS as readonly string[]).includes(value);

export const clusterStatusFilterLabel = (filter: ClusterStatusFilter, t: TFunction): string => {
  switch (filter) {
    case 'progressing':
      return t('Progressing');
    case 'ready':
      return t('Ready');
    case 'failed':
      return t('Failed');
    case 'deleting':
      return t('Deleting');
    case 'delete-failed':
      return t('Delete failed');
  }
};

export const isClusterSpecFilterValue = (value: string): value is string => value.trim().length > 0;

const CARD_CREATED_FORMAT = new Intl.DateTimeFormat('en-US', {
  month: 'short',
  day: 'numeric',
  hour: 'numeric',
  minute: '2-digit',
});

export const formatClusterCreatedAt = (value?: Metadata): string => {
  const timestamp = value?.creationTimestamp;
  if (!timestamp?.seconds) {
    return '—';
  }

  const date = new Date(
    Number(timestamp.seconds) * 1000 + Math.floor((timestamp.nanos ?? 0) / 1_000_000),
  );
  if (Number.isNaN(date.getTime())) {
    return '—';
  }

  return CARD_CREATED_FORMAT.format(date);
};
