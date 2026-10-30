import type { TFunction } from 'i18next';

import { type ComputeInstance, ComputeInstanceState } from '@osac/types';

export const INSTANCE_TYPE_UPDATE_MASK = ['spec.instance_type'] as const;

export const isStoppedVm = (vm: ComputeInstance): boolean =>
  vm.status?.state === ComputeInstanceState.STOPPED;

export const instanceTypeUpdateSuccessTitle = (
  t: TFunction,
  label: string,
  stopped: boolean,
): string =>
  stopped
    ? t('Instance type change submitted for {{label}}. Resize will apply on next start.', {
        label,
      })
    : t('Instance type change submitted for {{label}}.', { label });

export const instanceTypeUpdateWarningsDescription = (warnings: string[] | undefined): string =>
  (warnings ?? [])
    .map((warning) => warning.trim())
    .filter(Boolean)
    .join(' ');
