import { ExternalIPPools } from '@osac/types/private';
import { useListAllResources } from '@osac/ui-components/api/use-resource';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { ExternalIpPoolsTable } from './ExternalIpPoolsTable';
import GatedCreateButton from '../networking/GatedCreateButton';
import NetworkingListEmptyState from '../networking/NetworkingListEmptyState';
import { useNetworkClassNotReadyReason } from '../networking/useNetworkClassNotReadyReason';

export const ExternalIpPoolsListPage = () => {
  const { t } = useTranslation();

  // The list has no page controls, so it loads every page.
  const { data: poolsReply, isLoading, error } = useListAllResources(ExternalIPPools);
  const pools = poolsReply?.items ?? [];
  // A pool needs a READY network class.
  const networkClassNotReadyReason = useNetworkClassNotReadyReason();
  const createButton = (
    <GatedCreateButton
      service={ExternalIPPools}
      to="/admin/infrastructure/external-ip-pools/create"
      disabledReason={networkClassNotReadyReason}
    >
      {t('Create external IP pool')}
    </GatedCreateButton>
  );

  return (
    <ListPage
      title={t('External IP pools')}
      description={t('Manage external IP address pools for this cloud platform.')}
      error={error}
      actions={createButton}
    >
      <ListPageBody isLoading={isLoading} error={error}>
        {pools.length === 0 ? (
          <NetworkingListEmptyState
            emptyTitle={t('No external IP pools')}
            emptyBody={t('Create a pool of addresses that tenants allocate external IPs from.')}
            emptyAction={createButton}
          />
        ) : (
          <ExternalIpPoolsTable pools={pools} />
        )}
      </ListPageBody>
    </ListPage>
  );
};
