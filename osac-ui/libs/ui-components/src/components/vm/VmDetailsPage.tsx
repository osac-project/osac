import { useParams } from 'react-router-dom';

import { ComputeInstances } from '@osac/types';

import VmDetails from './DetailsPage/VmDetails';
import { getVmDetailTabLabels } from './vm-detail-tabs';
import { useGetResource } from '../../api/use-resource';
import { useTranslation } from '../../hooks/useTranslation';
import { ResourceDetailsPageError } from '../Resource/ResourceDetailsPageError';
import { ResourceDetailsPageLoading } from '../Resource/ResourceDetailsPageLoading';

export const VmDetailsPage = () => {
  const { t } = useTranslation();
  const { id = '' } = useParams() as { id: string };
  const { data, isLoading, isError, error, refetch } = useGetResource(ComputeInstances, { id });
  const vm = data?.object;

  if (isLoading) {
    return (
      <ResourceDetailsPageLoading
        parentTo="/vms"
        parentLabel={t('Virtual machines')}
        tabLabels={getVmDetailTabLabels(t)}
        tabsId="vm-detail-tabs-loading"
        cardCount={2}
      />
    );
  }

  if (isError) {
    return (
      <ResourceDetailsPageError
        parentTo="/vms"
        parentLabel={t('Virtual machines')}
        resourceLabel="virtual machine"
        error={error}
        onRetry={() => void refetch()}
      />
    );
  }

  if (!vm) {
    return (
      <ResourceDetailsPageError
        parentTo="/vms"
        parentLabel={t('Virtual machines')}
        resourceLabel="virtual machine"
        variant="not-found"
      />
    );
  }

  return <VmDetails vm={vm} />;
};
