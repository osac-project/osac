import { startTransition } from 'react';
import { useSearchParams } from 'react-router-dom';

import { SEARCH_PARAM, usePageFilter } from '../../hooks/use-page-filter';
import { useSession } from '../../hooks/use-session';

export const VIRTUAL_NETWORK_FILTER_PARAM = 'virtualNetwork';
export const TENANT_FILTER_PARAM = 'tenant';

interface FilterableResource {
  metadata?: { name?: string; tenant?: string; project?: string };
}

/**
 * URL-backed name, virtual network and tenant filters shared by the networking lists, plus
 * the console-wide project filter of the session.
 */
export const useNetworkingListFilters = () => {
  const [, setSearchParams] = useSearchParams();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [virtualNetworkId, setVirtualNetworkId] = usePageFilter(VIRTUAL_NETWORK_FILTER_PARAM);
  const [tenantId, setTenantId] = usePageFilter(TENANT_FILTER_PARAM);
  const { projects, setProjects } = useSession();

  const clearAll = () => {
    // The router applies URL changes in a transition. Clearing the projects in the same
    // transition keeps the project filter from writing back the URL it rendered with.
    if (projects.length > 0) {
      startTransition(() => setProjects([]));
    }
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete(SEARCH_PARAM);
        next.delete(VIRTUAL_NETWORK_FILTER_PARAM);
        next.delete(TENANT_FILTER_PARAM);
        return next;
      },
      { replace: true },
    );
  };

  /** Whether a resource passes the filters; `resourceVirtualNetworkId` is its parent network. */
  const matches = (resource: FilterableResource, resourceVirtualNetworkId?: string): boolean => {
    const name = resource.metadata?.name ?? '';
    return (
      (!search || name.toLowerCase().includes(search.toLowerCase())) &&
      (!virtualNetworkId || resourceVirtualNetworkId === virtualNetworkId) &&
      (!tenantId || resource.metadata?.tenant === tenantId) &&
      (projects.length === 0 || projects.includes(resource.metadata?.project ?? ''))
    );
  };

  return {
    search,
    setSearch,
    virtualNetworkId,
    setVirtualNetworkId,
    tenantId,
    setTenantId,
    isFiltered: Boolean(search || virtualNetworkId || tenantId || projects.length > 0),
    clearAll,
    matches,
  };
};

export type NetworkingListFilters = ReturnType<typeof useNetworkingListFilters>;
