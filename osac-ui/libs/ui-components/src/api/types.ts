import { UseQueryOptions } from '@tanstack/react-query';

import { type CelFilter } from './cel';

/**
 * All known API base routes. Adding a new resource hook requires adding its
 * route here first — unknown strings are rejected at compile time everywhere
 * a query key is constructed or used for cache operations.
 */
export type ApiRoute =
  | 'v1/compute_instances'
  | 'v1/compute_instance_templates'
  | 'v1/compute_instance_catalog_items'
  | 'v1/cluster_catalog_items'
  | 'v1/cluster_templates'
  | 'v1/cluster_versions'
  | 'v1/host_types'
  | 'v1/instance_types'
  | 'v1/disk_images'
  | 'v1/clusters'
  | 'v1/organizations'
  | 'v1/users'
  | 'v1/capabilities'
  | 'v1/virtual_networks'
  | 'v1/subnets'
  | 'v1/security_groups'
  | 'v1/baremetal_instance_catalog_items'
  | 'v1/baremetal_instances'
  | 'v1/external_ips'
  | 'v1/external_ip_attachments'
  | 'v1/external_ip_pools'
  | 'v1/console_sessions'
  | 'v1/storage_tiers'
  | 'v1/private/instance_types'
  | 'v1/private/baremetal_instance_types'
  | 'v1/private/tenants'
  | 'v1/private/storage_backends'
  | 'v1/private/storage_tiers'
  | 'v1/identity_providers'
  | 'v1/roles'
  | 'v1/role_bindings'
  | 'v1/projects'
  | 'v1/project_memberships';

/**
 * Strict 3-part tuple that encodes an API address.
 * The QueryClient in the app constructs the URL as:
 *   /<baseUrl>/<pathParams[0]>/<pathParams[1]>?<queryParams>
 */
export type ApiQueryKey = [
  baseUrl: ApiRoute,
  pathParams?: (string | number | undefined)[] | undefined,
  queryParams?: Record<string, unknown>,
];

export type UseApiQueryOptions<TQueryFnData, TError, TData> = Omit<
  UseQueryOptions<TQueryFnData, TError, TData, ApiQueryKey>,
  'queryKey' | 'meta'
> & {
  queryKey: ApiQueryKey;
};

/**
 * Type-safe factory for query keys used in cache operations
 * (invalidateQueries, refetchQueries, setQueryData, etc.).
 *
 * @example
 * qc.invalidateQueries({ queryKey: apiQueryKey('v1/compute_instances') });
 */
export const apiQueryKey = (
  baseUrl: ApiQueryKey[0],
  pathParams?: ApiQueryKey[1],
  queryParams?: ApiQueryKey[2],
): ApiQueryKey => {
  const path = Array.isArray(pathParams) && pathParams.length > 0 ? pathParams : undefined;
  const query =
    queryParams != null && typeof queryParams === 'object' && Object.keys(queryParams).length > 0
      ? queryParams
      : undefined;

  if (query !== undefined) {
    return [baseUrl, path, query];
  }
  if (path !== undefined) {
    return [baseUrl, path];
  }
  return [baseUrl];
};

export type ApiQueryParams = Record<string, string | number | boolean | null | undefined>;

export type ListParams = {
  filter?: CelFilter;
  limit?: number;
  offset?: number;
  order?: string;
};

/** Normalized list response from paginated list RPCs (`items`, `size`, `total`). */
export type ApiListResult<T> = {
  items: T[];
  size: number;
  total: number;
};
