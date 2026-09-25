import type { TFunction } from 'i18next';

import { type ExternalIP, ExternalIPState } from '@osac/types';
import type { ExternalIpAttachedTargetKind } from '@osac/ui-components/api/v1/external-ip-data';

export const attachedTargetKindLabel = (
  t: TFunction,
  kind: ExternalIpAttachedTargetKind,
): string => {
  switch (kind) {
    case 'baremetalInstance':
      return t('Bare metal');
    case 'cluster':
      return t('Cluster');
    case 'natGateway':
      return t('NAT gateway');
    case 'computeInstance':
      return t('Virtual machine');
  }
};

export const deleteDisabledReason = (externalIp: ExternalIP, t: TFunction): string | undefined => {
  if (externalIp.status?.state === ExternalIPState.EXTERNAL_IP_STATE_DELETING) {
    return t('This external IP is being deleted.');
  }
  return externalIp.status?.attached === true
    ? t('Detach this external IP before deleting it.')
    : undefined;
};
