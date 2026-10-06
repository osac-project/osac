import { useCallback, useMemo, useRef } from 'react';
import { useSearchParams } from 'react-router-dom';

export const SEARCH_PARAM = 'search';

export const serializePageFilter = (filters: string[]): string | null =>
  filters.length > 0 ? filters.join(',') : null;

const parseTypeFilters = <T extends string>(
  filterKey: string,
  searchParams: URLSearchParams,
  typeGuard: (val: string) => val is T,
): T[] => {
  const raw = searchParams.get(filterKey);
  if (!raw) {
    return [];
  }
  const filters = new Set<T>();
  for (const value of raw.split(',')) {
    const trimmed = value.trim();
    if (typeGuard(trimmed)) {
      filters.add(trimmed);
    }
  }
  return [...filters];
};

export const useArrayPageFilter = <T extends string>(
  filterKey: string,
  typeGuard: (val: string) => val is T,
): [T[], (filter: T) => void, VoidFunction] => {
  const [searchParams, setSearchParams] = useSearchParams();

  const filter = useMemo(
    () => parseTypeFilters<T>(filterKey, searchParams, typeGuard),
    [filterKey, searchParams, typeGuard],
  );

  // Track the latest effective filter so consecutive toggles fired before a
  // re-render compose instead of overwriting each other. react-router passes the
  // render-captured search params to the updater, so `prev` alone is stale
  // across synchronous calls.
  const filterRef = useRef(filter);
  filterRef.current = filter;

  const setFilter = useCallback(
    (value: T) => {
      const current = filterRef.current;
      const val = current.includes(value)
        ? current.filter((f) => f !== value)
        : [...current, value];
      filterRef.current = val;
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          const newVal = serializePageFilter(val);
          if (newVal === null) {
            next.delete(filterKey);
          } else {
            next.set(filterKey, newVal);
          }
          return next;
        },
        { replace: true },
      );
    },
    [filterKey, setSearchParams],
  );

  const clear = () => {
    filterRef.current = [];
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete(filterKey);
        return next;
      },
      { replace: true },
    );
  };

  return [filter, setFilter, clear];
};

export const usePageFilter = (filterKey: string): [string, (filter: string) => void] => {
  const [searchParams, setSearchParams] = useSearchParams();

  const filter = searchParams.get(filterKey) || '';

  const setFilter = useCallback(
    (value: string) => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          const trimmed = value.trim();
          if (!trimmed) {
            next.delete(filterKey);
          } else {
            next.set(filterKey, value);
          }
          return next;
        },
        { replace: true },
      );
    },
    [filterKey, setSearchParams],
  );

  return [filter, setFilter];
};
