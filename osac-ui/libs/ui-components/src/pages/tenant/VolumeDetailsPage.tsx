import { useParams } from 'react-router-dom';

import { Volumes } from '@osac/types';
import { useApiFetch } from '@osac/ui-components/api/api-context';
import { apiQueryKey } from '@osac/ui-components/api/types';
import { useApiQuery } from '@osac/ui-components/api/use-api-query';
import { ResourceDetailsPageError } from '@osac/ui-components/components/Resource/ResourceDetailsPageError';
import { ResourceDetailsPageLoading } from '@osac/ui-components/components/Resource/ResourceDetailsPageLoading';
import VolumeDetails from '@osac/ui-components/components/Volume/VolumeDetails';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

export const VolumeDetailsPage = () => {
  const { t } = useTranslation();
  const { id } = useParams() as { id: string };
  const client = useApiFetch(Volumes);

  const {
    data: volume,
    isLoading,
    isError,
    error,
    refetch,
  } = useApiQuery({
    queryKey: apiQueryKey('v1/volumes', [id]),
    queryFn: () => client.get({ id }),
    select: (data) => data.object,
    enabled: Boolean(id),
  });

  if (isLoading) {
    return (
      <ResourceDetailsPageLoading
        parentTo="/storage/volumes"
        parentLabel={t('Volumes')}
        cardCount={1}
      />
    );
  }

  if (isError) {
    return (
      <ResourceDetailsPageError
        parentTo="/storage/volumes"
        parentLabel={t('Volumes')}
        resourceLabel={t('volume')}
        error={error}
        onRetry={() => void refetch()}
      />
    );
  }

  if (!volume) {
    return (
      <ResourceDetailsPageError
        parentTo="/storage/volumes"
        parentLabel={t('Volumes')}
        resourceLabel={t('volume')}
        variant="not-found"
      />
    );
  }

  return <VolumeDetails volume={volume} />;
};
