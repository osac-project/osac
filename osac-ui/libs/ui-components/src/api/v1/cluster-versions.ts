import { Code, ConnectError } from '@connectrpc/connect';

import {
  type ClusterVersion,
  type ClusterVersionReference,
  ClusterVersionState,
  ClusterVersions,
} from '@osac/types';

import { cel } from '../cel';
import { type ListParams } from '../types';
import { useGetResource, useListAllResources, useListResource } from '../use-resource';
import { matchesClusterVersionReference } from './cluster-version-reference';

type ClusterVersionsQueryOptions = {
  enabled?: boolean;
};

export const CLUSTER_VERSION_ACTIVE_LIST_FILTER = cel<ClusterVersion>((filter) =>
  filter.and(
    filter.or(
      filter.field('spec.state').equals(ClusterVersionState.ACTIVE),
      filter.field('spec.state').equals(ClusterVersionState.DEPRECATED),
    ),
    filter.field('spec.enabled').equals(true),
  ),
);

// References both spec.state and spec.enabled so the public List RPC returns
// every version — including obsolete and disabled ones. The server injects
// per-field default predicates (active/deprecated + enabled) only for fields the
// caller's filter does not reference, so touching both fields defeats that hiding.
//
// The enum field spec.state must be compared with `==` (enumerated per state),
// NOT `in [...]`: the backend filter translator only resolves enum int literals
// to their stored string name for `==`/`!=`, so `state in [0,1,2,3]` becomes a
// `text in (0,1,2,3)` SQL clause that Postgres rejects ("failed to list").
export const CLUSTER_VERSION_ALL_STATES_LIST_FILTER = cel<ClusterVersion>((filter) =>
  filter.and(
    filter.or(
      filter.field('spec.state').equals(ClusterVersionState.UNSPECIFIED),
      filter.field('spec.state').equals(ClusterVersionState.ACTIVE),
      filter.field('spec.state').equals(ClusterVersionState.DEPRECATED),
      filter.field('spec.state').equals(ClusterVersionState.OBSOLETE),
    ),
    filter.or(
      filter.field('spec.enabled').equals(true),
      filter.field('spec.enabled').equals(false),
    ),
  ),
);

// Scopes the all-states catalog fetch to just the versions referenced by the
// given names, so the join stays correct once the List RPC paginates — a plain
// all-states fetch could page past the version a cluster references. The name
// list uses `metadata.name in [...]` (a string field — safe for `in`, unlike
// enum fields; see OSAC-4206), AND-ed with the all-states predicates so
// obsolete/disabled versions still resolve.
export const clusterVersionNamesFilter = (names: string[]) =>
  cel<ClusterVersion>((filter) =>
    filter.and(filter.field('metadata.name').isIn(names), CLUSTER_VERSION_ALL_STATES_LIST_FILTER),
  );

export const useClusterVersions = (
  params: ListParams = {},
  options: ClusterVersionsQueryOptions = {},
) =>
  useListResource(
    ClusterVersions,
    params,
    { enabled: options.enabled ?? true },
    (reply) => reply.items,
  );

export const useClusterVersion = (id: string | undefined) =>
  useGetResource(
    ClusterVersions,
    { id: id ?? '' },
    { enabled: Boolean(id) },
    (reply) => reply.object,
  );

export const useConfiguredClusterVersion = (
  reference: ClusterVersionReference | undefined,
  ownerTenant?: string,
) => {
  const id = reference?.id;
  const name = reference?.name.trim() || undefined;
  const byId = useClusterVersion(id);
  const byName = useListAllResources(
    ClusterVersions,
    name ? clusterVersionNamesFilter([name]) : undefined,
    { enabled: !id && Boolean(name) },
  );
  const query = id ? byId : byName;
  const isLoading = query.isLoading || query.isPlaceholderData;
  const notFound = query.error instanceof ConnectError && query.error.code === Code.NotFound;
  const isError = query.isError && !notFound;
  const candidates = id ? (byId.data ? [byId.data] : []) : (byName.data ?? []);
  const matches = reference
    ? candidates.filter(
        (version) =>
          matchesClusterVersionReference(reference, version) &&
          (ownerTenant === undefined ||
            version.metadata?.tenant === ownerTenant ||
            version.metadata?.tenant === 'shared'),
      )
    : [];

  return {
    data: query.isSuccess && !isLoading && matches.length === 1 ? matches[0] : undefined,
    isLoading,
    isError,
    error: isError ? query.error : undefined,
  };
};
