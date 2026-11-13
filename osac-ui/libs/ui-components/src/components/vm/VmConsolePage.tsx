import { useParams } from 'react-router-dom';

import { ComputeInstances } from '@osac/types';

import VmConsoleTab from './console/VmConsoleTab';
import { useGetResource } from '../../api/use-resource';
import { useTranslation } from '../../hooks/useTranslation';
import ResourceDetailsPage from '../Resource/ResourceDetailsPage';

import './VmConsolePage.css';

export const VmConsolePage = () => {
  const { t } = useTranslation();
  const { id = '' } = useParams<{ id: string }>();
  const query = useGetResource(ComputeInstances, { id }, { enabled: Boolean(id) });
  const vm = query.data?.object;

  return (
    <div className="vm-console-page">
      <ResourceDetailsPage
        isLoading={query.isLoading}
        error={query.error}
        found={Boolean(vm)}
        refetch={() => void query.refetch()}
        parentTo="/vms"
        parentLabel={t('Virtual machines')}
        resourceLabel="virtual machine"
        sectionClassName="vm-console-fallback-section"
      >
        {vm ? <VmConsoleTab vm={vm} /> : null}
      </ResourceDetailsPage>
    </div>
  );
};

export default VmConsolePage;
