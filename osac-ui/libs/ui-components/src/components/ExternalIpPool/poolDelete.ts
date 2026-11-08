import type { TFunction } from 'i18next';

import type { ExternalIPPool } from '@osac/types/private';

/**
 * Why the pool cannot be deleted yet, or undefined. The server rejects the delete while any
 * ExternalIP of the pool exists; `status.allocated` counts the ones holding an address.
 */
export const poolDeleteDisabledReason = (
  t: TFunction,
  pool: ExternalIPPool,
): string | undefined => {
  const allocated = Number(pool.status?.allocated ?? 0n);
  return allocated > 0
    ? t('Delete its {{count}} external IP first.', { count: allocated })
    : undefined;
};
