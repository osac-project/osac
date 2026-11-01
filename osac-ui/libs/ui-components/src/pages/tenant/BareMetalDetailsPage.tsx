import { useParams } from 'react-router-dom';

import { useBareMetalInstance } from '@osac/ui-components/api/v1/baremetal-instance';
import BareMetalDetails from '@osac/ui-components/components/BareMetalInstance/BareMetalDetails';
import { ResourceDetailsPageError } from '@osac/ui-components/components/Resource/ResourceDetailsPageError';
import { ResourceDetailsPageLoading } from '@osac/ui-components/components/Resource/ResourceDetailsPageLoading';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

export const BareMetalDetailsPage = () => {
  const { t } = useTranslation();
  const { id } = useParams() as { id: string };
  const { data: instance, isLoading, isError, error, refetch } = useBareMetalInstance(id);

  if (isLoading) {
    return (
      <ResourceDetailsPageLoading
        parentTo="/bare-metal"
        parentLabel={t('Bare Metal')}
        cardCount={3}
      />
    );
  }

  if (isError) {
    return (
      <ResourceDetailsPageError
        parentTo="/bare-metal"
        parentLabel={t('Bare Metal')}
        resourceLabel={t('bare metal instance')}
        error={error}
        onRetry={() => void refetch()}
      />
    );
  }

  if (!instance) {
    return (
      <ResourceDetailsPageError
        parentTo="/bare-metal"
        parentLabel={t('Bare Metal')}
        resourceLabel={t('bare metal instance')}
        variant="not-found"
      />
    );
  }

  return <BareMetalDetails instance={instance} />;
};
