import { useParams } from 'react-router-dom';
import { PageSection } from '@patternfly/react-core';

import { ComputeInstances } from '@osac/types';

import VmConsoleTab from './console/VmConsoleTab';
import { useGetResource } from '../../api/use-resource';
import { useTranslation } from '../../hooks/useTranslation';
import ResourceDetailsPage from '../Resource/ResourceDetailsPage';

export const VmConsolePage = () => {
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
      resourceLabel="virtual machine"
    >
      {vm ? (
        <PageSection hasBodyWrapper={false} isFilled>
          <VmConsoleTab vm={vm} />
        </PageSection>
      ) : null}
    </ResourceDetailsPage>
  );
};

export default VmConsolePage;
