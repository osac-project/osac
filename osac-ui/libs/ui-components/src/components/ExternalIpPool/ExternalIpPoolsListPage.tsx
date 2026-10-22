import { ExternalIPPools } from '@osac/types/private';
import { useListResource } from '@osac/ui-components/api/use-resource';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import { SubtleContent } from '@osac/ui-components/components/SubtleContent/SubtleContent';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { ExternalIpPoolsTable } from './ExternalIpPoolsTable';

export const ExternalIpPoolsListPage = () => {
  const { t } = useTranslation();

  const { data, isLoading, error } = useListResource(ExternalIPPools);
  const pools = data?.items ?? [];

  return (
    <ListPage
      title={t('External IP pools')}
      description={t('Manage external IP address pools for this cloud platform.')}
      error={error}
      actions={
        <CreateButton to="/admin/infrastructure/external-ip-pools/create">
          {t('Create pool')}
        </CreateButton>
      }
    >
      <ListPageBody isLoading={isLoading} error={error}>
        {pools.length === 0 ? (
          <SubtleContent component="p">
            {t('No external IP pools yet. Create one to get started.')}
          </SubtleContent>
        ) : (
          <ExternalIpPoolsTable pools={pools} />
        )}
      </ListPageBody>
    </ListPage>
  );
};
