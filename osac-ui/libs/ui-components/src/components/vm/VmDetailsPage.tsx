import { useParams } from 'react-router-dom';

import { ComputeInstances } from '@osac/types';

import VmDetails from './DetailsPage/VmDetails';
import { useGetResource } from '../../api/use-resource';
import { useTranslation } from '../../hooks/useTranslation';
import ResourceDetailsPage from '../Resource/ResourceDetailsPage';

export const VmDetailsPage = () => {
  const { t } = useTranslation();
  const { id = '' } = useParams<{ id: string }>();
  const query = useGetResource(ComputeInstances, { id }, { enabled: Boolean(id) });
  const vm = query.data?.object;

  return (
    <ResourceDetailsPage
      isLoading={query.isLoading}
      error={query.error}
      found={Boolean(vm)}
      refetch={() => void query.refetch()}
      parentTo="/vms"
      parentLabel={t('Virtual machines')}
      resourceLabel="virtual machine"
    >
      {vm ? <VmDetails vm={vm} /> : null}
    </ResourceDetailsPage>
  );
};
