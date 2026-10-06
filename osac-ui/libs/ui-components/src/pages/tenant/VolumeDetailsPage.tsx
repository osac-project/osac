import { useParams } from 'react-router-dom';

import { Volumes } from '@osac/types';
import { useGetResource } from '@osac/ui-components/api/use-resource';
import ResourceDetailsPage from '@osac/ui-components/components/Resource/ResourceDetailsPage';
import VolumeDetails from '@osac/ui-components/components/Volume/VolumeDetails';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

export const VolumeDetailsPage = () => {
  const { t } = useTranslation();
  const { id = '' } = useParams<{ id: string }>();
  const { data, isLoading, error, refetch } = useGetResource(Volumes, { id });
  const volume = data?.object;

  return (
    <ResourceDetailsPage
      error={error}
      found={!!volume}
      isLoading={isLoading}
      parentLabel={t('Volumes')}
      parentTo="/storage/volumes"
      refetch={refetch}
      resourceLabel={t('volume')}
      cardCount={1}
    >
      {volume && <VolumeDetails volume={volume} />}
    </ResourceDetailsPage>
  );
};
