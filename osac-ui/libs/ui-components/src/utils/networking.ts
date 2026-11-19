import type { TFunction } from 'i18next';

export const defaultResourceDeleteReason = (t: TFunction): string =>
  t('Default networking resources are system-managed and cannot be deleted.');
