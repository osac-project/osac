import { useParams } from 'react-router-dom';

import { StorageTiers } from '@osac/types/private';
import { useGetResource } from '@osac/ui-components/api/use-resource';

import { ResourceDetailsPageError } from '../../components/Resource/ResourceDetailsPageError';
import { ResourceDetailsPageLoading } from '../../components/Resource/ResourceDetailsPageLoading';
import { StorageTierDetails } from '../../components/Storage/StorageTierDetails';
import { useTranslation } from '../../hooks/useTranslation';

const TIERS_LIST_PATH = '/admin/infrastructure/storage/tiers';

export const StorageTierDetailsPage = () => {
  const { t } = useTranslation();
  const { id } = useParams() as { id: string };
  const { data: tier, isLoading, isError, error, refetch } = useGetResource(StorageTiers, { id });

  if (isLoading) {
    return (
      <ResourceDetailsPageLoading
        parentTo={TIERS_LIST_PATH}
        parentLabel={t('Storage tiers')}
        cardCount={2}
      />
    );
  }

  if (isError) {
    return (
      <ResourceDetailsPageError
        parentTo={TIERS_LIST_PATH}
        parentLabel={t('Storage tiers')}
        resourceLabel={t('storage tier')}
        error={error}
        onRetry={() => void refetch()}
      />
    );
  }

  if (!tier?.object) {
    return (
      <ResourceDetailsPageError
        parentTo={TIERS_LIST_PATH}
        parentLabel={t('Storage tiers')}
        resourceLabel={t('storage tier')}
        variant="not-found"
      />
    );
  }

  return <StorageTierDetails tier={tier.object} />;
};
