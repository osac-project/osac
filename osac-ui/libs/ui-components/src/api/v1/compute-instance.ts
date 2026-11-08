import { type MessageInitShape } from '@bufbuild/protobuf';
import { type QueryClient, useMutation } from '@tanstack/react-query';

import {
  ComputeInstanceSchema,
  ComputeInstances,
  type ComputeInstancesListResponse,
} from '@osac/types';

import { useCreateResource } from '../use-resource';

export const POST_CREATE_LIST_POLL_MS = 500;
export const POST_CREATE_LIST_POLL_MAX_ATTEMPTS = 20;

export const pollComputeInstancesUntilListed = async (
  qc: QueryClient,
  instanceId: string,
  signal?: { cancelled: boolean },
): Promise<void> => {
  for (let attempt = 0; attempt < POST_CREATE_LIST_POLL_MAX_ATTEMPTS; attempt++) {
    if (signal?.cancelled) {
      return;
    }
    await qc.invalidateQueries({ queryKey: [ComputeInstances.typeName] });
    const listQueries = qc.getQueriesData<ComputeInstancesListResponse>({
      queryKey: [ComputeInstances.typeName, 'list'],
    });
    if (listQueries.some(([, data]) => data?.items?.some((v) => v.id === instanceId))) {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, POST_CREATE_LIST_POLL_MS));
  }
};

export const useProvisionComputeInstance = () => {
  const createComputeInstance = useCreateResource(ComputeInstances);
  return useMutation({
    mutationFn: (vm: MessageInitShape<typeof ComputeInstanceSchema>) =>
      createComputeInstance.mutateAsync({ object: vm }).then((r) => r.object),
  });
};

export type ComputeInstancePowerAction = 'start' | 'stop' | 'restart';
