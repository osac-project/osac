import type { TFunction } from 'i18next';

export type BareMetalPowerStateFilter =
  | 'running'
  | 'stopped'
  | 'provisioning'
  | 'starting'
  | 'failing';

export const BARE_METAL_POWER_STATE_FILTERS: readonly BareMetalPowerStateFilter[] = [
  'running',
  'stopped',
  'provisioning',
  'starting',
  'failing',
];

export const isBareMetalPowerStateFilter = (value: string): value is BareMetalPowerStateFilter =>
  (BARE_METAL_POWER_STATE_FILTERS as readonly string[]).includes(value);

export const bareMetalPowerStateFilterLabel = (
  filter: BareMetalPowerStateFilter,
  t: TFunction,
): string => {
  switch (filter) {
    case 'running':
      return t('Running');
    case 'stopped':
      return t('Stopped');
    case 'provisioning':
      return t('Provisioning');
    case 'starting':
      return t('Starting');
    case 'failing':
      return t('Failed');
  }
};
