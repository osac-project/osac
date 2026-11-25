import type { TFunction } from 'i18next';

import { type Subnet, SubnetState } from '@osac/types';

import { networkingResourceOrigin } from './networking-resource-origin';
import { defaultResourceDeleteReason } from '../../utils/networking';

export interface SubnetDeleteGuardInput {
  subnet: Subnet;
  /**
   * Names of the workloads attached to the subnet. `undefined` means the caller
   * could not load them, in which case Delete stays enabled and the server's
   * guard message decides.
   */
  workloadNames?: readonly string[];
}

export interface SubnetDeleteGuard {
  canDelete: boolean;
  disabledReason?: string;
}

export const subnetDeleteGuard = (
  t: TFunction,
  { subnet, workloadNames }: SubnetDeleteGuardInput,
): SubnetDeleteGuard => {
  const origin = networkingResourceOrigin(subnet);
  if (origin === 'default') {
    return { canDelete: false, disabledReason: defaultResourceDeleteReason(t) };
  }
  if (origin === 'auto') {
    return { canDelete: false, disabledReason: t('Auto-created subnets cannot be deleted.') };
  }
  if (subnet.status?.state === SubnetState.DELETING) {
    return { canDelete: false, disabledReason: t('This subnet is being deleted.') };
  }
  if (workloadNames && workloadNames.length > 0) {
    return {
      canDelete: false,
      disabledReason: t('In use by {{names}}. Detach the workload before deleting it.', {
        names: workloadNames.join(', '),
      }),
    };
  }
  return { canDelete: true };
};
