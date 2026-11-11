import type { ExternalIP } from '@osac/types';

import { type ResourceSelectValue, emptyResourceSelectValue } from '../../Form/ResourceSelectField';

export const EXTERNAL_IPS_LIST_PATH = '/networking/external-ips';

export interface ExternalIpFormValues {
  metadata: {
    name: string;
    project: string;
    /** Asked of a provider admin only; tenant users create in their own tenant. */
    tenant?: ResourceSelectValue;
  };
  pool: ResourceSelectValue;
}

export const getExternalIpValues = (externalIp?: ExternalIP): ExternalIpFormValues => {
  if (externalIp) {
    return {
      metadata: {
        name: externalIp.metadata?.name ?? '',
        project: externalIp.metadata?.project ?? '',
      },
      pool: {
        id: externalIp.spec?.pool?.id ?? '',
        name: externalIp.spec?.pool?.name ?? '',
      },
    };
  }

  return {
    metadata: {
      name: '',
      project: '',
    },
    pool: emptyResourceSelectValue(),
  };
};
