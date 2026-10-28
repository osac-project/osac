import { type MessageInitShape } from '@bufbuild/protobuf';
import { timestampNow } from '@bufbuild/protobuf/wkt';
import { useMutation } from '@tanstack/react-query';

import {
  type ComputeInstance,
  ComputeInstanceSchema,
  ComputeInstanceState,
  ComputeInstances,
  type ComputeInstancesListResponse,
} from '@osac/types';
import { ComputeInstanceRunStrategy } from '@osac/types';
import { useProjectFilterQuery } from '@osac/ui-components/hooks/use-project-filter-query';

import {
  type ResourceQueryClient,
  useCreateResource,
  useDeleteResource,
  useGetResource,
  useListResource,
  useUpdateResource,
} from '../use-resource';

export const useComputeInstances = () => {
  const filter = useProjectFilterQuery<ComputeInstance>();
  return useListResource<
    typeof ComputeInstances.method.list.input,
    typeof ComputeInstances.method.list.output,
    ComputeInstance[]
  >(ComputeInstances, filter ? { filter } : {}, {}, (data) => data.items);
};

export const useComputeInstance = (id: string) => {
  const trimmedId = id?.trim() ?? '';
  const query = useGetResource(
    ComputeInstances,
    { id: trimmedId },
    { enabled: Boolean(trimmedId) },
  );

  return { ...query, data: query.data?.object };
};

export const invalidateComputeInstancesQueries = async (qc: ResourceQueryClient) => {
  await qc.invalidateQueries([ComputeInstances.typeName]);
};

export const POST_CREATE_LIST_POLL_MS = 500;
export const POST_CREATE_LIST_POLL_MAX_ATTEMPTS = 20;

export const pollComputeInstancesUntilListed = async (
  qc: ResourceQueryClient,
  instanceId: string,
  signal?: { cancelled: boolean },
): Promise<void> => {
  for (let attempt = 0; attempt < POST_CREATE_LIST_POLL_MAX_ATTEMPTS; attempt++) {
    if (signal?.cancelled) {
      return;
    }
    await invalidateComputeInstancesQueries(qc);
    const listQueries = qc.getQueriesData<ComputeInstancesListResponse>([
      ComputeInstances.typeName,
      'list',
    ]);
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

export type PatchComputeInstanceInput = {
  id: string;
  powerAction: ComputeInstancePowerAction;
};

const buildPowerPatchBody = (
  powerAction: ComputeInstancePowerAction,
): MessageInitShape<typeof ComputeInstanceSchema> => {
  switch (powerAction) {
    case 'stop':
      return {
        spec: { runStrategy: ComputeInstanceRunStrategy.COMPUTE_INSTANCE_RUN_STRATEGY_HALTED },
        status: { state: ComputeInstanceState.STOPPED },
      };
    case 'start':
      return {
        spec: { runStrategy: ComputeInstanceRunStrategy.COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS },
        status: { state: ComputeInstanceState.RUNNING },
      };
    case 'restart':
      return { spec: { restartRequestedAt: timestampNow() } };
  }
};

export const usePatchComputeInstance = () => {
  const updateComputeInstance = useUpdateResource(ComputeInstances);
  return useMutation({
    mutationFn: ({ id, powerAction }: PatchComputeInstanceInput) => {
      const body = buildPowerPatchBody(powerAction);
      return updateComputeInstance.mutateAsync({ object: { id, ...body } }).then((r) => r.object);
    },
  });
};

export const useDeleteComputeInstance = () => {
  const deleteComputeInstance = useDeleteResource(ComputeInstances);
  return useMutation({
    mutationFn: (id: string) => deleteComputeInstance.mutateAsync({ id }),
  });
};
