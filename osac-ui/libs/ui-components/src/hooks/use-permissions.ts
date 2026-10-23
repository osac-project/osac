import type { DescMethod, DescService } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
// Permission checks use a descriptor-aware cache key rather than an ApiRoute.
// eslint-disable-next-line no-restricted-imports
import { useQuery } from '@tanstack/react-query';

import { SelfSubjectAccessReviews } from '@osac/types';

import { useSession } from './use-session';
import { useApiFetch } from '../api/api-context';

export type ServiceWithMethod<Method extends string> = Omit<DescService, 'method'> & {
  method: Record<Method, DescMethod>;
};

type ServiceMethod<Service extends DescService> = Extract<keyof Service['method'], string>;

const retryableCodes = new Set([Code.Unavailable, Code.DeadlineExceeded, Code.ResourceExhausted]);

const isRetryableError = (error: unknown): boolean =>
  error instanceof ConnectError && retryableCodes.has(error.code);

export interface UsePermissionsResult {
  allowed: boolean;
  error: Error | null;
  isLoading: boolean;
}

// Permission results are cached for the lifetime of the browser page so they
// are shared across components and refreshed only after a full page reload.
export const usePermissions = <Service extends DescService>(
  service: Service,
  method: ServiceMethod<Service>,
): UsePermissionsResult => {
  const { role } = useSession();
  const rpcMethodName = service.method[method].name;

  // SSAR allows checks for public API only.
  // Therefore if consumer of the hook tries to check private API
  // we return true/false based on user's role (admin always true, false otherwise)
  const isPrivateService = service.typeName.startsWith('osac.private.v1.');
  const client = useApiFetch(SelfSubjectAccessReviews);
  const { data, error, isPending } = useQuery({
    queryKey: ['permissions', service.typeName, rpcMethodName],
    enabled: !isPrivateService,
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

  if (isPrivateService) {
    return { allowed: role === 'admin', error: null, isLoading: false };
  }

  return {
    allowed: data?.object?.status?.allowed === true,
    error,
    isLoading: isPending,
  };
};
