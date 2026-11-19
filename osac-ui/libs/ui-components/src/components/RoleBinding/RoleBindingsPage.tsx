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

import { type Role, type RoleBinding, RoleBindings, Roles } from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import ListPageToolbar from '@osac/ui-components/components/Page/ListPageToolbar';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import DebounceSearchInput from '@osac/ui-components/components/Primitives/DebounceSearchInput';
import ViewSwitcher from '@osac/ui-components/components/Primitives/ViewSwitcher';
import {
  SEARCH_PARAM,
  useArrayPageFilter,
  usePageFilter,
} from '@osac/ui-components/hooks/use-page-filter';
import { useViewType } from '@osac/ui-components/hooks/use-view-type';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import {
  buildRoleBindingListFilter,
  isRoleBindingRoleFilter,
  isRoleBindingStatusFilter,
} from './ListPage/role-binding-list-filter';
import RoleBindingCard from './ListPage/RoleBindingCard';
import RoleBindingRoleFilter from './ListPage/RoleBindingRoleFilter';
import RoleBindingsTable from './ListPage/RoleBindingsTable';
import RoleBindingStatusFilter from './ListPage/RoleBindingStatusFilter';
import { resolveRoleTitle } from './utils';

const STATUS_FILTER_PARAM = 'status';
const ROLE_FILTER_PARAM = 'role';
const ROLE_BINDINGS_VIEW_KEY = 'role-bindings-list';

const EMPTY_ROLE_BINDINGS: RoleBinding[] = [];
const EMPTY_ROLES: Role[] = [];

const RoleBindingsPage = () => {
  const { t } = useTranslation();
  const [, setSearchParams] = useSearchParams();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [searchResetKey, setSearchResetKey] = useState(0);
  const [statusFilters, toggleStatusFilter] = useArrayPageFilter(
    STATUS_FILTER_PARAM,
    isRoleBindingStatusFilter,
  );
  const [roleFilters, toggleRoleFilter] = useArrayPageFilter(
    ROLE_FILTER_PARAM,
    isRoleBindingRoleFilter,
  );
  const [viewType] = useViewType(ROLE_BINDINGS_VIEW_KEY);

  const listFilter = useMemo(
    () =>
      buildRoleBindingListFilter({
        search,
        statuses: statusFilters,
        roleIds: roleFilters,
      }),
    [search, statusFilters, roleFilters],
  );

  const {
    data: listResponse,
    isLoading,
    error,
  } = useListResource(RoleBindings, { filter: listFilter });
  const roleBindings = listResponse?.items ?? EMPTY_ROLE_BINDINGS;

  const {
    data: totalsResponse,
    isLoading: totalsLoading,
    error: totalsError,
  } = useListResource(RoleBindings, { limit: 0 });
  const totalItems = !totalsLoading && !totalsError ? (totalsResponse?.total ?? 0) : 0;

  const { data: roles, error: rolesError } = useListResource(Roles);

  const rolesById = useMemo(() => {
    const map = new Map<string, string>();
    if (roles?.items) {
      for (const role of roles.items) {
        map.set(role.id, role.spec?.title || role.metadata?.name || role.id);
      }
    }
    return map;
  }, [roles?.items]);

  const isFiltered = Boolean(search.trim() || statusFilters.length || roleFilters.length);
  const showEmptyState = !isLoading && !error && roleBindings.length === 0;

  const clearAllFilters = () => {
    setSearchResetKey((key) => key + 1);
    requestAnimationFrame(() => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          next.delete(SEARCH_PARAM);
          next.delete(STATUS_FILTER_PARAM);
          next.delete(ROLE_FILTER_PARAM);
          return next;
        },
        { replace: true },
      );
    });
  };

  const emptyState =
    totalItems === 0 && !isFiltered ? (
      <EmptyState titleText={t('No role bindings found')} headingLevel="h2">
        <EmptyStateBody>{t('No role bindings are available yet.')}</EmptyStateBody>
      </EmptyState>
    ) : (
      <EmptyState titleText={t('No role bindings match your filters')} headingLevel="h2">
        <EmptyStateBody>
          {t('Try a different status, role, or search term.')}{' '}
          <Button variant="link" isInline onClick={clearAllFilters}>
            {t('Clear all filters')}
          </Button>
        </EmptyStateBody>
      </EmptyState>
    );

  const filteredText =
    totalsLoading || totalsError || roleBindings.length === totalItems
      ? t('{{count}} role binding', { count: roleBindings.length })
      : t('{{shown}} of {{count}} role binding', {
          shown: roleBindings.length,
          count: totalItems,
        });

  return (
    <ListPage
      title={t('Role Bindings')}
      description={t('Manage role bindings for users.')}
      error={error}
      actions={
        <CreateButton service={RoleBindings} to="create">
          {t('Create role binding')}
        </CreateButton>
      }
    >
      <Stack hasGutter>
        <StackItem>
          <ListPageToolbar
            filteredText={!isLoading && !error && !showEmptyState ? filteredText : undefined}
            isFiltered={isFiltered}
            onClearAllFilters={clearAllFilters}
          >
            <Toolbar>
              <ToolbarContent>
                <ToolbarItem>
                  <RoleBindingStatusFilter
                    statusFilters={statusFilters}
                    onToggle={toggleStatusFilter}
                  />
                </ToolbarItem>
                <ToolbarItem>
                  <RoleBindingRoleFilter
                    roles={roles?.items ?? EMPTY_ROLES}
                    roleFilters={roleFilters}
                    onToggle={toggleRoleFilter}
                    loadError={rolesError}
                  />
                </ToolbarItem>
                <ToolbarItem>
                  <DebounceSearchInput
                    placeholder={t('Search by name')}
                    value={search}
                    onChange={setSearch}
                    resetKey={searchResetKey}
                    aria-label={t('Filter role bindings by name')}
                  />
                </ToolbarItem>
                <ToolbarGroup align={{ default: 'alignEnd' }}>
                  <ToolbarItem>
                    <ViewSwitcher pageKey={ROLE_BINDINGS_VIEW_KEY} />
                  </ToolbarItem>
                </ToolbarGroup>
              </ToolbarContent>
            </Toolbar>
          </ListPageToolbar>
        </StackItem>
        <StackItem>
          <ListPageBody isLoading={isLoading} error={error}>
            {showEmptyState ? (
              emptyState
            ) : viewType === 'cards' ? (
              <Gallery hasGutter minWidths={{ default: '400px' }} maxWidths={{ default: '400px' }}>
                {roleBindings.map((roleBinding) => (
                  <GalleryItem key={roleBinding.id}>
                    <RoleBindingCard
                      roleBinding={roleBinding}
                      roleTitle={resolveRoleTitle(roleBinding, rolesById)}
                    />
                  </GalleryItem>
                ))}
              </Gallery>
            ) : (
              <RoleBindingsTable roleBindings={roleBindings} rolesById={rolesById} />
            )}
          </ListPageBody>
        </StackItem>
      </Stack>
    </ListPage>
  );
};

export default RoleBindingsPage;
