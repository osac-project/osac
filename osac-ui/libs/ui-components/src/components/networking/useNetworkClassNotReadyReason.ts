import { NetworkClassState, NetworkClasses } from '@osac/types/private';

import { useListResource } from '../../api/use-resource';
import { useSession } from '../../hooks/use-session';
import { useTranslation } from '../../hooks/useTranslation';

/**
 * Why a VirtualNetwork or ExternalIPPool cannot be created yet: the server needs a READY
 * NetworkClass. Only the provider admin can read the NetworkClass; for other roles, and
 * while it loads or fails to load, this is undefined and the server decides.
 */
export const useNetworkClassNotReadyReason = (): string | undefined => {
  const { t } = useTranslation();
  const { role } = useSession();
  const isAdmin = role === 'admin';
  const { data, isSuccess } = useListResource(NetworkClasses, undefined, { enabled: isAdmin });
  if (!isAdmin || !isSuccess) {
    return undefined;
  }
  return data.items[0]?.status?.state === NetworkClassState.READY
    ? undefined
    : t('Available when the network class is Ready.');
};
