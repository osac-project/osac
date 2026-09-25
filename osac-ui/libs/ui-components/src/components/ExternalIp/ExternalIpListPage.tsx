import {
  SearchInput,
  Toolbar,
  ToolbarContent,
  ToolbarGroup,
  ToolbarItem,
} from '@patternfly/react-core';

import { type ExternalIP } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import { useExternalIpsData } from '@osac/ui-components/api/v1/external-ip-data';
import { SEARCH_PARAM, usePageFilter } from '@osac/ui-components/hooks/use-page-filter';
import { useProjectFilterQuery } from '@osac/ui-components/hooks/use-project-filter-query';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { EXTERNAL_IPS_LIST_PATH } from './CreatePage/values';
import ExternalIpsTable from './ExternalIpsTable';
import ListPage from '../Page/ListPage';
import ListPageBody from '../Page/ListPageBody';
import ProjectFilter from '../Page/ProjectFilter';
import CreateButton from '../Primitives/CreateButton.tsx';
import { SubtleContent } from '../SubtleContent/SubtleContent';

const ExternalIpListPage = () => {
  const { t } = useTranslation();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const projectFilter = useProjectFilterQuery<ExternalIP>();
  const { externalIps, poolsById, attachedTargetsByExternalIpId, isLoading, error } =
    useExternalIpsData({
      filter: cel<ExternalIP>((filter) =>
        filter.and(
          projectFilter,
          search
            ? filter.or(
                filter.field('metadata.name').contains(search),
                filter.field('status.address').contains(search),
              )
            : undefined,
        ),
      ),
    });

  return (
    <ListPage
      title={t('External IPs')}
      description={t(
        'Review provider-assigned IP pools and allocate external addresses for edge exposure.',
      )}
      error={error}
      actions={
        <CreateButton to={`${EXTERNAL_IPS_LIST_PATH}/create`}>
          {t('Create external IP')}
        </CreateButton>
      }
    >
      <ListPageBody isLoading={isLoading} error={error}>
        <Toolbar>
          <ToolbarContent>
            <ToolbarGroup>
              <ToolbarItem>
                <ProjectFilter />
              </ToolbarItem>
              <ToolbarItem>
                <SearchInput
                  placeholder={t('Search external IPs')}
                  value={search}
                  onChange={(_e, v) => setSearch(v)}
                  onClear={() => setSearch('')}
                  aria-label={t('Search external IPs')}
                />
              </ToolbarItem>
            </ToolbarGroup>
          </ToolbarContent>
        </Toolbar>
        {externalIps.length === 0 ? (
          <SubtleContent component="p">
            {search || projectFilter
              ? t('No external IPs match your search.')
              : t('No external IPs yet. Create one to get started.')}
          </SubtleContent>
        ) : (
          <ExternalIpsTable
            externalIps={externalIps}
            poolsById={poolsById}
            attachedTargetsByExternalIpId={attachedTargetsByExternalIpId}
          />
        )}
      </ListPageBody>
    </ListPage>
  );
};

export default ExternalIpListPage;
