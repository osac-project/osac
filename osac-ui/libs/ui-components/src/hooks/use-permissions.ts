import type { DescMethod, DescService } from '@bufbuild/protobuf';
import { type Client, Code, ConnectError } from '@connectrpc/connect';
// Permission checks use a descriptor-aware cache key rather than an ApiRoute.
// eslint-disable-next-line no-restricted-imports
import { queryOptions, useQueries, useQuery } from '@tanstack/react-query';

import { SelfSubjectAccessReviews, type SelfSubjectAccessReviewsCreateResponse } from '@osac/types';

import { useSession } from './use-session';
import { useApiFetch } from '../api/api-context';

export type ServiceWithMethod<Method extends string> = Omit<DescService, 'method'> & {
  method: Record<Method, DescMethod>;
};

type ServiceMethod<Service extends DescService> = Extract<keyof Service['method'], string>;

const retryableCodes = new Set([Code.Unavailable, Code.DeadlineExceeded, Code.ResourceExhausted]);

const isRetryableError = (error: unknown): boolean =>
  error instanceof ConnectError && retryableCodes.has(error.code);

// SSAR only checks public APIs; private APIs fall back to the user's role.
const permissionQueryOptions = (
  service: DescService,
  rpcMethodName: string,
  client: Client<typeof SelfSubjectAccessReviews>,
) =>
  queryOptions<SelfSubjectAccessReviewsCreateResponse, Error>({
    queryKey: ['permissions', service.typeName, rpcMethodName] as const,
    enabled: !service.typeName.startsWith('osac.private.v1.'),
    queryFn: async () => {
      const response = await client.create({
        object: {
          spec: {
            service: service.typeName,
            method: rpcMethodName,
          },
        },
      });

      if (!response.object?.status) {
        throw new Error('SelfSubjectAccessReview response is missing object or status');
      }

      return response;
    },
    retry: (failureCount, error) => failureCount < 2 && isRetryableError(error),
    retryOnMount: false,
    staleTime: Infinity,
    gcTime: Infinity,
    refetchOnMount: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    refetchInterval: false,
  });

export interface UsePermissionsResult {
  allowed: boolean;
  error: Error | null;
  isLoading: boolean;
}

export const usePermissionsForServices = <Service extends DescService>(
  services: Service[],
  method: ServiceMethod<Service>,
): UsePermissionsResult[] => {
  const { role } = useSession();
  const client = useApiFetch(SelfSubjectAccessReviews);
  const results = useQueries({
    queries: services.map((service) =>
      permissionQueryOptions(service, service.method[method].name, client),
    ),
  });

  return services.map((service, index) => {
    if (service.typeName.startsWith('osac.private.v1.')) {
      return { allowed: role === 'admin', error: null, isLoading: false };
    }

    const result = results[index];
    return {
      allowed: result?.data?.object?.status?.allowed === true,
      error: result?.error ?? null,
      isLoading: result?.isPending ?? true,
    };
  });
};

// Permission results are cached for the lifetime of the browser page so they
// are shared across components and refreshed only after a full page reload.
export const usePermissions = <Service extends DescService>(
  service: Service,
  method: ServiceMethod<Service>,
): UsePermissionsResult => {
  const { role } = useSession();
  const isPrivateService = service.typeName.startsWith('osac.private.v1.');
  const rpcMethodName = service.method[method].name;
  const client = useApiFetch(SelfSubjectAccessReviews);
  const result = useQuery(permissionQueryOptions(service, rpcMethodName, client));

  if (isPrivateService) {
    return { allowed: role === 'admin', error: null, isLoading: false };
  }

  return {
    allowed: result.data?.object?.status?.allowed === true,
    error: result.error,
    isLoading: result.isPending,
  };
};
