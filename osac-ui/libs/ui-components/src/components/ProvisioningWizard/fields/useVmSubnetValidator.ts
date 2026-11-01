import { useCallback } from 'react';

import { SubnetState, Subnets } from '@osac/types';

import { useApiFetch } from '../../../api/api-context';
import { useTranslation } from '../../../hooks/useTranslation';
import type { ResourceSelectValue } from '../../Form/resourceSelectValue';

export const useVmSubnetValidator = () => {
  const client = useApiFetch(Subnets);
  const { t } = useTranslation();

  return useCallback(
    async (value: ResourceSelectValue, virtualNetworkId: string) => {
      if (!value?.id) {
        return undefined;
      }
      const message = t('A subnet must be READY and support compute instances.');
      try {
        const { object: subnet } = await client.get({ id: value.id });
        return subnet?.id === value.id &&
          !subnet.metadata?.deletionTimestamp &&
          subnet.status?.state === SubnetState.READY &&
          subnet.status.computeInstanceSupported === true &&
          (!virtualNetworkId || subnet.spec?.virtualNetwork?.id === virtualNetworkId)
          ? undefined
          : message;
      } catch {
        return message;
      }
    },
    [client, t],
  );
};
