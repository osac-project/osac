# API Query Architecture

## Overview

The API layer communicates with the backend via [Connect](https://connectrpc.com/) (gRPC-Web). TypeScript types are generated from protobuf schemas (`@osac/types`), and every RPC call is fully typed end-to-end.

The layer is split across two concerns:

- **`ui-components`** — declares *what* to fetch/write using typed hooks. It never contains transport configuration, base URLs, or credentials.
- **The app** (e.g. `app-frontend`) — owns the Connect `Transport`, `ApiProvider`, and `QueryClient`, which together define *how* RPCs reach the backend.

This separation means any hook from `ui-components` works in any app that supplies a compatible `ApiProvider` and `QueryClient`, without touching the hook itself.

---

## Connect Transport and `ApiProvider`

`ApiProvider` stores a Connect `Transport` in React context. Hooks obtain a typed gRPC client from it via `useApiFetch(ServiceDescriptor)`.

```ts
// api-context.tsx

export const ApiProvider = ({ transport, children }: ApiProviderProps) => (
  <ApiContext.Provider value={{ transport }}>{children}</ApiContext.Provider>
);

export const useApiFetch = <T extends DescService>(service: T): Client<T> => {
  const ctx = useContext(ApiContext);
  return useMemo(() => createClient(service, ctx.transport), [service, ctx.transport]);
};
```

`useApiFetch` accepts any protobuf service descriptor (e.g. `ComputeInstances`, `Clusters`) and returns a memoized Connect `Client<T>` with fully typed methods for every RPC in that service (`list`, `get`, `create`, `update`, `delete`, etc.).

### Error interceptor

`connectErrorInterceptor` is a Connect interceptor that converts `ConnectError(Unauthenticated)` into an `UnauthorizedError`, which the app's error boundary handles as a login redirect.

---

## `ApiRoute` and Cache Keys

Every resource route is listed in the `ApiRoute` union in `types.ts`. Using an unknown string anywhere a route is expected is a **compile error**. When adding a new resource, register its route here first.

```ts
export type ApiRoute =
  | 'v1/compute_instances'
  | // add new routes here
```

`ApiQueryKey` encodes a cache address as a 3-part tuple:

```ts
export type ApiQueryKey = [
  baseUrl: ApiRoute,                         // [0] must be a known ApiRoute
  pathParams?: (string | number)[] | null,   // [1] e.g. ['abc-123'] for a single item
  queryParams?: Record<string, ...>          // [2] e.g. { filter: '...', limit: 20 }
];
```

Use `apiQueryKey()` to construct keys — it enforces the tuple shape at compile time.

---

## Reads — `useApiQuery`

`useApiQuery` is a thin wrapper around TanStack's `useQuery` that constrains the key to `ApiQueryKey`. Each hook provides its own `queryFn` that calls the Connect client:

```ts
import { useApiFetch } from '../api-context';
import { type ListParams, apiQueryKey } from '../types';
import { useApiQuery } from '../use-api-query';

export const useComputeInstances = (params: ListParams = {}) => {
  const client = useApiFetch(ComputeInstances);
  return useApiQuery({
    queryKey: apiQueryKey('v1/compute_instances', null, params),
    queryFn: () => client.list(params),
    select: (data: ComputeInstancesListResponse) => data.items,
  });
};

export const useComputeInstance = (id: string) => {
  const client = useApiFetch(ComputeInstances);
  return useApiQuery({
    queryKey: apiQueryKey('v1/compute_instances', [id]),
    queryFn: () => client.get({ id }),
    select: (data) => data.object,
    enabled: Boolean(id),
  });
};
```

### `ListParams`

All list hooks share a single `ListParams` type defined in `types.ts`:

```ts
export type ListParams = {
  filter?: string;
  limit?: number;
  offset?: number;
  order?: string;
};
```

### Default `QueryClient` settings

| Option | Value | Rationale |
|---|---|---|
| `staleTime` | 5 s | Short window to avoid redundant re-fetches on mount |
| `refetchInterval` | 30 s | Background polling for out-of-band changes |
| `refetchOnMount` | `true` | Always validate on component mount |
| `retry` | 1 | One retry on transient network errors |

---

## Writes — Connect Client Mutations

Mutation hooks also use `useApiFetch(ServiceDescriptor)` to get the Connect client, then call its typed RPC methods directly:

```ts
import { useApiFetch } from '../api-context';
import { useApiQueryClient } from '../use-api-query';
import { useMutation } from '@tanstack/react-query';

export const useProvisionComputeInstance = () => {
  const client = useApiFetch(ComputeInstances);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: (vm: MessageInitShape<typeof ComputeInstanceSchema>) =>
      client.create({ object: vm }).then((r) => r.object!),
    onSuccess: () => invalidateComputeInstancesQueries(qc),
  });
};

export const useDeleteComputeInstance = () => {
  const client = useApiFetch(ComputeInstances);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: (id: string) => client.delete({ id }),
    onSuccess: () => invalidateComputeInstancesQueries(qc),
  });
};
```

---

## Cache Operations — `useApiQueryClient`

`useApiQueryClient` is a typed wrapper around TanStack's `useQueryClient`. It constrains all query key arguments to `ApiQueryKey`, catching route typos at compile time. An ESLint rule enforces its use everywhere outside the definition file.

```ts
export type ApiQueryClient = {
  invalidateQueries: (filters: { queryKey: ApiQueryKey }) => Promise<void>;
  refetchQueries: (filters: { queryKey: ApiQueryKey }) => Promise<void>;
  cancelQueries: (filters: { queryKey: ApiQueryKey }) => Promise<void>;
  getQueryData: <T>(queryKey: ApiQueryKey) => T | undefined;
  setQueryData: <T>(queryKey: ApiQueryKey, updater: Updater<T>) => T | undefined;
  setQueriesData: <T>(filters: { queryKey: ApiQueryKey }, updater: Updater<T>) => void;
};
```

---

## Adding a new resource

### 1. Generate types

Define the protobuf service and messages in the proto schema, then run:

```bash
make -C ../proto generate
```

This generates the service descriptor and TypeScript types in `@osac/types`,
alongside the corresponding backend generated code so all generated outputs
remain in sync.

### 2. Register the route

Add the route to `ApiRoute` in `types.ts`:

```ts
export type ApiRoute =
  | 'v1/compute_instances'
  | 'v1/some_resources'; // new
```

### 3. Add query and mutation hooks

Create `libs/ui-components/src/api/v1/<resource>.ts`:

```ts
import { useMutation } from '@tanstack/react-query';

import { SomeResources, type SomeResourcesListResponse } from '@osac/types';

import { useApiFetch } from '../api-context';
import { type ListParams, apiQueryKey } from '../types';
import { type ApiQueryClient, useApiQuery, useApiQueryClient } from '../use-api-query';

export const useSomeResources = (params: ListParams = {}) => {
  const client = useApiFetch(SomeResources);
  return useApiQuery({
    queryKey: apiQueryKey('v1/some_resources', null, params),
    queryFn: () => client.list(params),
    select: (data: SomeResourcesListResponse) => data.items,
  });
};

export const useSomeResource = (id: string) => {
  const client = useApiFetch(SomeResources);
  return useApiQuery({
    queryKey: apiQueryKey('v1/some_resources', [id]),
    queryFn: () => client.get({ id }),
    select: (data) => data.object,
    enabled: Boolean(id),
  });
};

const invalidateSomeResourcesQueries = (qc: ApiQueryClient) =>
  qc.invalidateQueries({ queryKey: apiQueryKey('v1/some_resources') });

export const useCreateSomeResource = () => {
  const client = useApiFetch(SomeResources);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: (data) => client.create({ object: data }).then((r) => r.object!),
    onSuccess: () => invalidateSomeResourcesQueries(qc),
  });
};
```

Key rules:

1. **`ApiRoute` first** — add the route to `types.ts` before writing any hook.
2. **Reads use `useApiQuery`** — provide a `queryFn` that calls the Connect client.
3. **Both reads and writes use `useApiFetch(ServiceDescriptor)`** — returns a typed `Client<T>` with RPC methods.
4. **Cache operations use `useApiQueryClient` + `apiQueryKey`** — never pass raw string keys to `invalidateQueries` etc.
5. **`select`** — unwrap `.items` or `.object` here, not in the component.
6. **`ListParams`** — use the shared type from `types.ts` for list hook parameters.

---

## App-level setup (`main.tsx`)

The app is responsible for:

1. **Creating the Connect transport** — with the base URL and interceptors (e.g. `connectErrorInterceptor` for auth).
2. **Passing it to `ApiProvider`** — so hooks can create typed gRPC clients via `useApiFetch()`.
3. **Creating the `QueryClient`** — with default options for caching and polling.

```tsx
import { createConnectTransport } from '@connectrpc/connect-web';
import { ApiProvider, connectErrorInterceptor } from '@osac/ui-components/api/api-context';

const connectTransport = createConnectTransport({
  baseUrl: '/api/fulfillment',
  interceptors: [connectErrorInterceptor],
});

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      staleTime: 5_000,
      refetchOnMount: true,
      refetchInterval: 30_000,
    },
  },
});

// Render:
<ApiProvider transport={connectTransport}>
  <QueryClientProvider client={queryClient}>
    <App />
  </QueryClientProvider>
</ApiProvider>
```

An app targeting a different backend only needs to replace the transport configuration — all `ui-components` hooks continue to work unchanged.
