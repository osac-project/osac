import { UseQueryResult, useQuery, useQueryClient } from '@tanstack/react-query';

import type { ApiQueryKey, UseApiQueryOptions } from './types';

export const useApiQuery = <TQueryFnData = unknown, TData = TQueryFnData>(
  options: UseApiQueryOptions<TQueryFnData, unknown, TData>,
): UseQueryResult<TData, unknown> => useQuery(options);

type Updater<T> = T | ((old: T | undefined) => T | undefined);

/**
 * Typed wrapper around QueryClient. All methods that accept a query key are
 * constrained to ApiQueryKey so the route must be a known ApiRoute value.
 *
 * Use useApiQueryClient() instead of useQueryClient() everywhere outside of
 * this file. An ESLint rule enforces this.
 */
export type ApiQueryClient = {
  /** Mark matching queries stale and trigger a background refetch. */
  invalidateQueries: (filters: { queryKey: ApiQueryKey }) => Promise<void>;
  /** Invalidate all queries keyed by a Connect service descriptor (e.g. useListResource). */
  invalidateServiceQueries: (service: { typeName: string }) => Promise<void>;
  /** Immediately refetch matching active queries. */
  refetchQueries: (filters: { queryKey: ApiQueryKey }) => Promise<void>;
  /** Cancel any in-flight fetches for matching queries. */
  cancelQueries: (filters: { queryKey: ApiQueryKey }) => Promise<void>;
  /** Read a single cached entry. */
  getQueryData: <T>(queryKey: ApiQueryKey) => T | undefined;
  /** Write a single cached entry. */
  setQueryData: <T>(queryKey: ApiQueryKey, updater: Updater<T>) => T | undefined;
  /** Write all cached entries whose key matches the filter. */
  setQueriesData: <T>(filters: { queryKey: ApiQueryKey }, updater: Updater<T>) => void;
};

export const useApiQueryClient = (): ApiQueryClient => {
  const qc = useQueryClient();

  return {
    invalidateQueries: (filters) => qc.invalidateQueries(filters),
    invalidateServiceQueries: (service) => qc.invalidateQueries({ queryKey: [service.typeName] }),
    refetchQueries: (filters) => qc.refetchQueries(filters),
    cancelQueries: (filters) => qc.cancelQueries(filters),
    getQueryData: <T>(queryKey: ApiQueryKey) => qc.getQueryData<T>(queryKey),
    setQueryData: <T>(queryKey: ApiQueryKey, updater: Updater<T>) =>
      qc.setQueryData<T>(queryKey, updater),
    setQueriesData: <T>(filters: { queryKey: ApiQueryKey }, updater: Updater<T>) => {
      qc.setQueriesData<T>(filters, updater);
    },
  };
};
