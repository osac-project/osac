import { Alert, Toolbar, ToolbarContent, ToolbarGroup, ToolbarItem } from '@patternfly/react-core';

import { useClusters } from '@osac/ui-components/api/v1/cluster';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';

import { ClustersTable } from './ClustersTable';
import { useTranslation } from '../../hooks/useTranslation';
import ListPage from '../Page/ListPage';
import ListPageBody from '../Page/ListPageBody';
import ProjectFilter from '../Page/ProjectFilter';

export const ClustersPage = () => {
  const { t } = useTranslation();
  const { data: clusters = [], isLoading, error } = useClusters();

  return (
    <ListPage
      title={t('Clusters')}
      description={t('OpenShift clusters provisioned for your organization.')}
      error={error}
      actions={<CreateButton to="/clusters/create">{t('Create cluster')}</CreateButton>}
    >
      <ListPageBody isLoading={isLoading} error={error}>
        <Toolbar>
          <ToolbarContent>
            <ToolbarGroup>
              <ToolbarItem>
                <ProjectFilter />
              </ToolbarItem>
            </ToolbarGroup>
          </ToolbarContent>
        </Toolbar>
        {clusters.length === 0 ? (
          <Alert variant="info" isInline title="No clusters found">
            No clusters are provisioned for your organization yet.
          </Alert>
        ) : (
          <ClustersTable clusters={clusters} />
        )}
      </ListPageBody>
    </ListPage>
  );
};
