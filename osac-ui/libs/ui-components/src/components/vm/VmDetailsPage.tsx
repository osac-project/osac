import { useParams } from 'react-router-dom';

import { ComputeInstances } from '@osac/types';

import VmDetails from './DetailsPage/VmDetails';
import { getVmDetailTabLabels } from './vm-detail-tabs';
import { useGetResource } from '../../api/use-resource';
import { useTranslation } from '../../hooks/useTranslation';
import ResourceDetailsPage from '../Resource/ResourceDetailsPage';

export const VmDetailsPage = () => {
  const { t } = useTranslation();
  const { id } = useParams() as { id: string };
  const trimmedId = id?.trim() ?? '';
  const query = useGetResource(
    ComputeInstances,
    { id: trimmedId },
    { enabled: Boolean(trimmedId) },
  );
  const vm = query.data?.object;

  return (
    <ResourceDetailsPage
      isLoading={query.isLoading}
      error={query.error}
      found={Boolean(vm)}
      refetch={() => void query.refetch()}
      parentTo="/vms"
      parentLabel={t('Virtual machines')}
      tabLabels={getVmDetailTabLabels(t)}
      cardCount={2}
      resourceLabel="virtual machine"
    >
      {vm ? <VmDetails vm={vm} /> : null}
    </ResourceDetailsPage>
  );
};
