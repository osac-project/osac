import type { TFunction } from 'i18next';

import { type Metadata } from '@osac/types';

const CARD_CREATED_FORMAT = new Intl.DateTimeFormat(undefined, {
  dateStyle: 'medium',
  timeStyle: 'short',
});

export type VmPowerStateFilter =
  | 'starting'
  | 'running'
  | 'failed'
  | 'deleting'
  | 'stopping'
  | 'stopped'
  | 'paused';

export const VM_POWER_STATE_FILTERS: readonly VmPowerStateFilter[] = [
  'starting',
  'running',
  'failed',
  'deleting',
  'stopping',
  'stopped',
  'paused',
];

export const isVmPowerStateFilter = (value: string): value is VmPowerStateFilter =>
  (VM_POWER_STATE_FILTERS as readonly string[]).includes(value);

export const vmPowerStateFilterLabel = (filter: VmPowerStateFilter, t: TFunction): string => {
  switch (filter) {
    case 'starting':
      return t('Starting');
    case 'running':
      return t('Running');
    case 'failed':
      return t('Failed');
    case 'deleting':
      return t('Deleting');
    case 'stopping':
      return t('Stopping');
    case 'stopped':
      return t('Stopped');
    case 'paused':
      return t('Paused');
  }
};

export const formatVmCreatedAt = (value?: Metadata): string => {
  const timestamp = value?.creationTimestamp;
  if (!timestamp?.seconds) {
    return '-';
  }

  const date = new Date(
    Number(timestamp.seconds) * 1000 + Math.floor((timestamp.nanos ?? 0) / 1_000_000),
  );
  if (Number.isNaN(date.getTime())) {
    return '-';
  }

  return CARD_CREATED_FORMAT.format(date);
};
