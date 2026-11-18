// Options for queries that preload a resource a wizard was opened with: fetch
// once (or reuse the cached snapshot as-is) and never refresh in the
// background. Query options are per-observer, so every observer of the query
// must pass these — a single defaulting observer re-enables background
// refetch for the query. A background refetch would surface an error or swap
// the cached item under an in-progress form and discard user input.
export const wizardPreloadQueryOptions = {
  refetchInterval: false,
  refetchOnWindowFocus: false,
  refetchOnReconnect: false,
  refetchOnMount: false,
} as const;
