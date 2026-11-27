import { useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  Button,
  EmptyState,
  EmptyStateBody,
  Stack,
  StackItem,
  Toolbar,
  ToolbarContent,
  ToolbarGroup,
  ToolbarItem,
} from '@patternfly/react-core';

import { type Subnet, Subnets, VirtualNetworks } from '@osac/types';
import { useListAllResources } from '@osac/ui-components/api/use-resource';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import ProjectFilter from '@osac/ui-components/components/Page/ProjectFilter';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import DebounceSearchInput from '@osac/ui-components/components/Primitives/DebounceSearchInput';
import { SEARCH_PARAM, usePageFilter } from '@osac/ui-components/hooks/use-page-filter.ts';
import { useProjectFilterQuery } from '@osac/ui-components/hooks/use-project-filter-query';
import { useSession } from '@osac/ui-components/hooks/use-session';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { useNetworkConsumers } from '../../api/v1/networking-consumers';
import {
  SUBNETS_CREATE_PATH,
  VIRTUAL_NETWORK_FILTER_PARAM,
  buildSubnetListFilter,
} from '../../components/networking/subnet-list-utils';
import SubnetActionsMenu from '../../components/networking/SubnetActionsMenu';
import { SubnetsTable } from '../../components/networking/SubnetsTable';
import VirtualNetworkFilter from '../../components/networking/VirtualNetworkFilter';

export const SubnetsListPage = () => {
  const { t } = useTranslation();
  const { projects, setProjects } = useSession();
  const [searchParams, setSearchParams] = useSearchParams();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [searchResetKey, setSearchResetKey] = useState(0);

  const virtualNetworkId = searchParams.get(VIRTUAL_NETWORK_FILTER_PARAM) ?? '';
  const setVirtualNetworkId = (id: string) => {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (id) {
          next.set(VIRTUAL_NETWORK_FILTER_PARAM, id);
        } else {
          next.delete(VIRTUAL_NETWORK_FILTER_PARAM);
        }
        return next;
      },
      { replace: true },
    );
  };

  const projectFilter = useProjectFilterQuery<Subnet>();
  const filter = useMemo(
    () => buildSubnetListFilter(projectFilter, search.trim(), virtualNetworkId || undefined),
    [projectFilter, search, virtualNetworkId],
  );

  const { data, isLoading, error } = useListAllResources(Subnets, filter);
  const items = useMemo(() => data?.items ?? [], [data?.items]);

  const { data: virtualNetworksData } = useListAllResources(VirtualNetworks);
  const virtualNetworks = useMemo(
    () => virtualNetworksData?.items ?? [],
    [virtualNetworksData?.items],
  );
  const virtualNetworksById = useMemo(
    () => new Map(virtualNetworks.map((network) => [network.id, network])),
    [virtualNetworks],
  );

  const consumers = useNetworkConsumers();
  const consumersResolved = !consumers.isLoading && !consumers.failed;

  const isFiltered = Boolean(projects.length || search.trim() || virtualNetworkId);
  const showEmptyState = !isLoading && !error && items.length === 0;

  const clearAllFilters = () => {
    setSearchResetKey((key) => key + 1);
    setProjects([]);
    requestAnimationFrame(() => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          next.delete(SEARCH_PARAM);
          next.delete(VIRTUAL_NETWORK_FILTER_PARAM);
          return next;
        },
        { replace: true },
      );
    });
  };

  const emptyState = !isFiltered ? (
    <EmptyState titleText={t('No subnets found')} headingLevel="h2">
      <EmptyStateBody>{t('No subnets are available yet.')}</EmptyStateBody>
    </EmptyState>
  ) : (
    <EmptyState titleText={t('No subnets match your filters')} headingLevel="h2">
      <EmptyStateBody>
        {t('Try a different project, virtual network, or search term.')}{' '}
        <Button variant="link" isInline onClick={clearAllFilters}>
          {t('Clear all filters')}
        </Button>
      </EmptyStateBody>
    </EmptyState>
  );

  return (
    <ListPage
      title={t('Subnets')}
      description={t('Address ranges inside virtual networks.')}
      error={error}
      actions={
        <CreateButton service={Subnets} to={SUBNETS_CREATE_PATH}>
          {t('Create subnet')}
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
                <VirtualNetworkFilter
                  virtualNetworks={virtualNetworks}
                  selectedId={virtualNetworkId}
                  onChange={setVirtualNetworkId}
                />
                <ToolbarItem>
                  <DebounceSearchInput
                    placeholder={t('Search subnets by name')}
                    value={search}
                    onChange={(value) => setSearch(value)}
                    resetKey={searchResetKey}
                    aria-label={t('Search subnets by name')}
                  />
                </ToolbarItem>
              </ToolbarGroup>
            </ToolbarContent>
          </Toolbar>
        </StackItem>
        <StackItem>
          <ListPageBody isLoading={isLoading} error={error}>
            {showEmptyState ? (
              emptyState
            ) : (
              <SubnetsTable
                subnets={items}
                virtualNetworksById={virtualNetworksById}
                renderActions={(subnet: Subnet) => (
                  <SubnetActionsMenu
                    subnet={subnet}
                    workloadNames={
                      consumersResolved
                        ? (consumers.workloadNamesBySubnetId.get(subnet.id) ?? [])
                        : undefined
                    }
                  />
                )}
              />
            )}
          </ListPageBody>
        </StackItem>
      </Stack>
    </ListPage>
  );
};
