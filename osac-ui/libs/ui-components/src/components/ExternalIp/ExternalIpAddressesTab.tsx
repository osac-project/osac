import type { ReactNode } from 'react';
import { useSearchParams } from 'react-router-dom';
import { SearchInput, Toolbar, ToolbarContent, ToolbarItem } from '@patternfly/react-core';

import type { ExternalIP } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import { useExternalIpsData } from '@osac/ui-components/api/v1/external-ip-data';
import { resourceDisplayName } from '@osac/ui-components/api/v1/networking';
import { SEARCH_PARAM, usePageFilter } from '@osac/ui-components/hooks/use-page-filter';
import { useProjectFilterQuery } from '@osac/ui-components/hooks/use-project-filter-query';
import { useSession } from '@osac/ui-components/hooks/use-session';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import ExternalIpsTable from './ExternalIpsTable';
import NetworkingListEmptyState from '../networking/NetworkingListEmptyState';
import NetworkingSelectFilter from '../networking/NetworkingSelectFilter';
import { TENANT_FILTER_PARAM } from '../networking/useNetworkingListFilters';
import { useNetworkingTenants } from '../networking/useNetworkingTenants';
import ListPageBody from '../Page/ListPageBody';
import ProjectFilter from '../Page/ProjectFilter';

interface ExternalIpAddressesTabProps {
  /** The page's Create external IP action, offered by the empty state. */
  createAction?: ReactNode;
}

/** The external IP addresses, filtered on the server by project, tenant and search. */
const ExternalIpAddressesTab = ({ createAction }: ExternalIpAddressesTabProps) => {
  const { t } = useTranslation();
  const [, setSearchParams] = useSearchParams();
  const { setProjects } = useSession();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [tenantId, setTenantId] = usePageFilter(TENANT_FILTER_PARAM);
  const { isAdmin, tenants } = useNetworkingTenants();
  const projectFilter = useProjectFilterQuery<ExternalIP>();
  const { externalIps, poolsById, attachedTargetsByExternalIpId, isLoading, error } =
    useExternalIpsData({
      filter: cel<ExternalIP>((filter) =>
        filter.and(
          projectFilter,
          isAdmin && tenantId ? filter.field('metadata.tenant').equals(tenantId) : undefined,
          search
            ? filter.or(
                filter.field('metadata.name').contains(search),
                filter.field('status.address').contains(search),
              )
            : undefined,
        ),
      ),
    });

  const clearFilters = () => {
    setProjects([]);
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete(SEARCH_PARAM);
        next.delete(TENANT_FILTER_PARAM);
        return next;
      },
      { replace: true },
    );
  };

  return (
    <ListPageBody isLoading={isLoading} error={error}>
      <Toolbar>
        <ToolbarContent>
          {isAdmin ? (
            <ToolbarItem>
              <NetworkingSelectFilter
                allLabel={t('All tenants')}
                ariaLabel={t('Filter by tenant')}
                options={tenants.map((tenant) => ({
                  value: tenant.id,
                  label: resourceDisplayName(tenant.metadata, tenant.id),
                }))}
                selected={tenantId}
                onChange={setTenantId}
              />
            </ToolbarItem>
          ) : null}
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
        </ToolbarContent>
      </Toolbar>
      {externalIps.length === 0 ? (
        <NetworkingListEmptyState
          isFiltered={Boolean(search || projectFilter || (isAdmin && tenantId))}
          emptyTitle={t('No external IPs')}
          emptyBody={t('Create an external IP to allocate an address from a pool.')}
          emptyAction={createAction}
          onClearFilters={clearFilters}
        />
      ) : (
        <ExternalIpsTable
          externalIps={externalIps}
          poolsById={poolsById}
          attachedTargetsByExternalIpId={attachedTargetsByExternalIpId}
        />
      )}
    </ListPageBody>
  );
};

export default ExternalIpAddressesTab;
