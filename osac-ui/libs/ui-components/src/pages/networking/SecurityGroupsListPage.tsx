import { useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  Button,
  EmptyState,
  EmptyStateBody,
  Gallery,
  GalleryItem,
  Stack,
  StackItem,
  Toolbar,
  ToolbarContent,
  ToolbarGroup,
  ToolbarItem,
} from '@patternfly/react-core';

import { SecurityGroup, SecurityGroups, type VirtualNetwork, VirtualNetworks } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import { useListResource } from '@osac/ui-components/api/use-resource';
import {
  buildSecurityGroupListFilter,
  uniqueVirtualNetworkIds,
} from '@osac/ui-components/components/networking/security-group-list-utils';
import SecurityGroupCard from '@osac/ui-components/components/networking/SecurityGroupCard';
import { SecurityGroupsTable } from '@osac/ui-components/components/networking/SecurityGroupsTable';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import ProjectFilter from '@osac/ui-components/components/Page/ProjectFilter';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import DebounceSearchInput from '@osac/ui-components/components/Primitives/DebounceSearchInput';
import ViewSwitcher from '@osac/ui-components/components/Primitives/ViewSwitcher';
import { SEARCH_PARAM, usePageFilter } from '@osac/ui-components/hooks/use-page-filter.ts';
import { useProjectFilterQuery } from '@osac/ui-components/hooks/use-project-filter-query';
import { useSession } from '@osac/ui-components/hooks/use-session';
import { useViewType } from '@osac/ui-components/hooks/use-view-type';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { SecurityGroupCreateModal } from '../../components/networking/SecurityGroupCreateModal';

const SECURITY_GROUPS_VIEW_KEY = 'security-groups';

export const SecurityGroupsListPage = () => {
  const { t } = useTranslation();
  const { projects, setProjects } = useSession();
  const [, setSearchParams] = useSearchParams();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [viewType] = useViewType(SECURITY_GROUPS_VIEW_KEY);
  const [searchResetKey, setSearchResetKey] = useState(0);
  const [isCreateModalOpen, setIsCreateModalOpen] = useState(false);

  const projectFilter = useProjectFilterQuery<SecurityGroup>();

  const filter = useMemo(
    () => buildSecurityGroupListFilter(projectFilter, search.trim()),
    [projectFilter, search],
  );

  const { data, isLoading, error } = useListResource(SecurityGroups, { filter });

  const items = useMemo(() => data?.items ?? [], [data?.items]);

  const virtualNetworkIds = useMemo(() => uniqueVirtualNetworkIds(items), [items]);

  const { data: virtualNetworksData } = useListResource(
    VirtualNetworks,
    { filter: cel<VirtualNetwork>((vnFilter) => vnFilter.field('id').isIn(virtualNetworkIds)) },
    { enabled: virtualNetworkIds.length > 0 },
  );

  const virtualNetworksById = useMemo(
    () => new Map((virtualNetworksData?.items ?? []).map((vn) => [vn.id, vn])),
    [virtualNetworksData?.items],
  );

  const isFiltered = Boolean(projects.length || search.trim());
  const showEmptyState = !isLoading && !error && items.length === 0;

  const clearAllFilters = () => {
    setSearchResetKey((key) => key + 1);
    setProjects([]);
    requestAnimationFrame(() => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          next.delete(SEARCH_PARAM);
          return next;
        },
        { replace: true },
      );
    });
  };

  const emptyState = !isFiltered ? (
    <EmptyState titleText={t('No security groups found')} headingLevel="h2">
      <EmptyStateBody>{t('No security groups are available yet.')}</EmptyStateBody>
    </EmptyState>
  ) : (
    <EmptyState titleText={t('No security groups match your filters')} headingLevel="h2">
      <EmptyStateBody>
        {t('Try a different project or search term.')}{' '}
        <Button variant="link" isInline onClick={clearAllFilters}>
          {t('Clear all filters')}
        </Button>
      </EmptyStateBody>
    </EmptyState>
  );

  return (
    <>
      <ListPage
        title={t('Security groups')}
        description={t('Manage firewall rules for your virtual networks.')}
        error={error}
        actions={
          <CreateButton service={SecurityGroups} onClick={() => setIsCreateModalOpen(true)}>
            {t('Create security group')}
          </CreateButton>
        }
      >
        <Stack hasGutter>
          <StackItem>
            <Toolbar>
              <ToolbarContent>
                <ToolbarGroup>
                  <ToolbarItem>
                    <ProjectFilter />
                  </ToolbarItem>
                  <ToolbarItem>
                    <DebounceSearchInput
                      placeholder={t('Search security groups')}
                      value={search}
                      onChange={(v) => setSearch(v)}
                      resetKey={searchResetKey}
                      aria-label={t('Search security groups')}
                    />
                  </ToolbarItem>
                </ToolbarGroup>
                <ToolbarGroup align={{ default: 'alignEnd' }}>
                  <ToolbarItem>
                    <ViewSwitcher pageKey={SECURITY_GROUPS_VIEW_KEY} />
                  </ToolbarItem>
                </ToolbarGroup>
              </ToolbarContent>
            </Toolbar>
          </StackItem>
          <StackItem>
            <ListPageBody isLoading={isLoading} error={error}>
              {showEmptyState ? (
                emptyState
              ) : viewType === 'cards' ? (
                <Gallery hasGutter minWidths={{ default: '300px' }} maxWidths={{ default: '1fr' }}>
                  {items.map((securityGroup) => (
                    <GalleryItem key={securityGroup.id}>
                      <SecurityGroupCard
                        securityGroup={securityGroup}
                        virtualNetworksById={virtualNetworksById}
                      />
                    </GalleryItem>
                  ))}
                </Gallery>
              ) : (
                <SecurityGroupsTable
                  securityGroups={items}
                  virtualNetworksById={virtualNetworksById}
                />
              )}
            </ListPageBody>
          </StackItem>
        </Stack>
      </ListPage>

      {isCreateModalOpen && (
        <SecurityGroupCreateModal onClose={() => setIsCreateModalOpen(false)} />
      )}
    </>
  );
};
