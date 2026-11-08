import { SearchInput, Toolbar, ToolbarContent, ToolbarItem } from '@patternfly/react-core';

import NetworkingSelectFilter from './NetworkingSelectFilter';
import type { NetworkingListFilters } from './useNetworkingListFilters';
import { useNetworkingTenants } from './useNetworkingTenants';
import { resourceDisplayName } from '../../api/v1/networking';
import { useTranslation } from '../../hooks/useTranslation';
import ListPageToolbar from '../Page/ListPageToolbar';
import ProjectFilter from '../Page/ProjectFilter';

interface NetworkingListToolbarProps {
  filters: NetworkingListFilters;
  searchLabel: string;
  /** When set, the toolbar offers a Virtual network filter over these networks. */
  virtualNetworks?: Array<{ id: string; metadata?: { name?: string; tenant?: string } }>;
  shownCount: number;
  totalCount: number;
}

/** Name search, Project, Virtual network and (provider admin) Tenant filters of a networking list. */
const NetworkingListToolbar = ({
  filters,
  searchLabel,
  virtualNetworks,
  shownCount,
  totalCount,
}: NetworkingListToolbarProps) => {
  const { t } = useTranslation();
  const { isAdmin, tenants, tenantName } = useNetworkingTenants();

  return (
    <ListPageToolbar
      filteredText={
        filters.isFiltered
          ? t('{{shown}} of {{total}} shown', { shown: shownCount, total: totalCount })
          : undefined
      }
      isFiltered={filters.isFiltered}
      // With no rows, the empty state offers Clear all filters instead.
      onClearAllFilters={shownCount > 0 ? filters.clearAll : undefined}
    >
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
                selected={filters.tenantId}
                onChange={filters.setTenantId}
              />
            </ToolbarItem>
          ) : null}
          <ToolbarItem>
            <ProjectFilter />
          </ToolbarItem>
          {virtualNetworks ? (
            <ToolbarItem>
              <NetworkingSelectFilter
                allLabel={t('All virtual networks')}
                ariaLabel={t('Filter by virtual network')}
                options={virtualNetworks.map((vn) => ({
                  value: vn.id,
                  // Tenants reuse names such as "default", so the admin also sees the tenant.
                  label: isAdmin
                    ? t('{{name}} ({{tenant}})', {
                        name: resourceDisplayName(vn.metadata, vn.id),
                        tenant: tenantName(vn.metadata?.tenant),
                      })
                    : resourceDisplayName(vn.metadata, vn.id),
                }))}
                selected={filters.virtualNetworkId}
                onChange={filters.setVirtualNetworkId}
              />
            </ToolbarItem>
          ) : null}
          <ToolbarItem>
            <SearchInput
              placeholder={searchLabel}
              aria-label={searchLabel}
              value={filters.search}
              onChange={(_event, value) => filters.setSearch(value)}
              onClear={() => filters.setSearch('')}
            />
          </ToolbarItem>
        </ToolbarContent>
      </Toolbar>
    </ListPageToolbar>
  );
};

export default NetworkingListToolbar;
