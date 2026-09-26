import { type MessageInitShape } from '@bufbuild/protobuf';
import { keepPreviousData, useMutation } from '@tanstack/react-query';

import {
  type BareMetalInstance,
  type BareMetalInstanceCatalogItem,
  BareMetalInstanceCatalogItems,
  BareMetalInstanceRunStrategy,
  BareMetalInstanceSchema,
  BareMetalInstances,
} from '@osac/types';
import { useProjectFilterQuery } from '@osac/ui-components/hooks/use-project-filter-query';

import { useApiFetch } from '../api-context';
import { type ApiListResult, type ListParams, apiQueryKey } from '../types';
import { buildUpdateMaskPaths } from './update-mask';
import { type ApiQueryClient, useApiQuery, useApiQueryClient } from '../use-api-query';

export const useBareMetalInstances = () => {
  const client = useApiFetch(BareMetalInstances);
  const filter = useProjectFilterQuery<BareMetalInstance>();
  return useApiQuery({
    queryKey: apiQueryKey('v1/baremetal_instances', undefined, filter ? { filter } : undefined),
    queryFn: () => client.list({ filter }),
    select: (data) => data.items,
    placeholderData: keepPreviousData,
  });
};

export const useBareMetalInstance = (id: string) => {
  const client = useApiFetch(BareMetalInstances);
  return useApiQuery({
    queryKey: apiQueryKey('v1/baremetal_instances', [id]),
    queryFn: () => client.get({ id }),
    select: (data) => data.object,
    enabled: Boolean(id),
  });
};

export const useBareMetalInstanceCatalogItems = (params: ListParams = {}, enabled = true) => {
  const client = useApiFetch(BareMetalInstanceCatalogItems);
  return useApiQuery<
    Awaited<ReturnType<typeof client.list>>,
    ApiListResult<BareMetalInstanceCatalogItem>
  >({
    queryKey: apiQueryKey('v1/baremetal_instance_catalog_items', undefined, params),
    queryFn: () => client.list(params),
    placeholderData: keepPreviousData,
    select: (data) => ({
      items: data.items,
      size: data.size,
      total: data.total,
    }),
    enabled,
  });
};

export const useBareMetalInstanceCatalogItem = (id: string | undefined) => {
  const client = useApiFetch(BareMetalInstanceCatalogItems);
  const trimmedId = id?.trim() ?? '';
  return useApiQuery({
    queryKey: apiQueryKey(
      'v1/baremetal_instance_catalog_items',
      trimmedId ? [trimmedId] : undefined,
    ),
    queryFn: () => client.get({ id: trimmedId }),
    select: (data) => data.object,
    enabled: Boolean(trimmedId),
  });
};

export const invalidateBareMetalInstancesQueries = async (qc: ApiQueryClient) => {
  await qc.invalidateQueries({ queryKey: apiQueryKey('v1/baremetal_instances') });
};

export type BareMetalPowerAction = 'start' | 'stop' | 'restart';

export type PatchBareMetalInstanceInput =
  | { id: string; action: 'start' | 'stop' }
  | { id: string; action: 'restart'; currentTrigger: bigint };

const buildPatchBody = (
  input: PatchBareMetalInstanceInput,
): MessageInitShape<typeof BareMetalInstanceSchema> => {
  switch (input.action) {
    case 'start':
      return { spec: { runStrategy: BareMetalInstanceRunStrategy.ALWAYS } };
    case 'stop':
      return { spec: { runStrategy: BareMetalInstanceRunStrategy.HALTED } };
    case 'restart':
      return { spec: { restartTrigger: input.currentTrigger + 1n } };
  }
};

export const usePatchBareMetalInstance = () => {
  const client = useApiFetch(BareMetalInstances);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: (input: PatchBareMetalInstanceInput) => {
      const body = buildPatchBody(input);
      return client
        .update({
          object: { id: input.id, ...body },
          updateMask: { paths: buildUpdateMaskPaths(body as Record<string, unknown>) },
        })
        .then((r) => r.object);
    },
    onSuccess: () => invalidateBareMetalInstancesQueries(qc),
  });
};

export const useDeleteBareMetalInstance = () => {
  const client = useApiFetch(BareMetalInstances);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: (id: string) => client.delete({ id }),
    onSuccess: () => invalidateBareMetalInstancesQueries(qc),
  });
};

export const useCreateBareMetalInstance = () => {
  const client = useApiFetch(BareMetalInstances);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: (bmi: MessageInitShape<typeof BareMetalInstanceSchema>) =>
      client.create({ object: bmi }).then((r) => r.object),
    onSuccess: () => invalidateBareMetalInstancesQueries(qc),
  });
};
