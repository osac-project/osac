import type { TFunction } from 'i18next';

export type ComputeInstancePowerAction = 'start' | 'stop' | 'restart';

export const getPowerActionErrorTitle = (
  t: TFunction,
  action: ComputeInstancePowerAction,
  name: string,
): string => {
  switch (action) {
    case 'start':
      return t('Failed to start virtual machine {{name}}', { name });
    case 'stop':
      return t('Failed to stop virtual machine {{name}}', { name });
    case 'restart':
      return t('Failed to restart virtual machine {{name}}', { name });
  }
};

export const getPowerActionSuccessTitle = (
  t: TFunction,
  action: ComputeInstancePowerAction,
  name: string,
): string => {
  switch (action) {
    case 'start':
      return t('Start initiated for {{name}}', { name });
    case 'stop':
      return t('Stop initiated for {{name}}', { name });
    case 'restart':
      return t('Restart initiated for {{name}}', { name });
  }
};
