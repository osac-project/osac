import { type MessageInitShape } from '@bufbuild/protobuf';
import { useMutation } from '@tanstack/react-query';

import {
  type BareMetalInstance,
  BareMetalInstanceCatalogItems,
  BareMetalInstanceRunStrategy,
  BareMetalInstanceSchema,
  BareMetalInstances,
} from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';
import { useProjectFilterQuery } from '@osac/ui-components/hooks/use-project-filter-query';

import { useApiFetch } from '../api-context';
import { type CelFilter, cel } from '../cel';
import { apiQueryKey } from '../types';
import { buildUpdateMaskPaths } from './update-mask';
import { type ApiQueryClient, useApiQuery, useApiQueryClient } from '../use-api-query';

export const useBareMetalInstances = (filters?: CelFilter<BareMetalInstance>, enabled = true) => {
  const projectFilter = useProjectFilterQuery<BareMetalInstance>();
  const filter = cel<BareMetalInstance>((builder) => builder.and(projectFilter, filters));
  const totalsFilter = cel<BareMetalInstance>((builder) => builder.and(projectFilter));

  const filteredBmsQuery = useListResource(BareMetalInstances, { filter }, { enabled });
  const totalBmsQuery = useListResource(
    BareMetalInstances,
    { filter: totalsFilter, limit: 0 },
    { enabled },
  );

  const isLoading = filteredBmsQuery.isLoading || totalBmsQuery.isLoading;
  const error = filteredBmsQuery.error || totalBmsQuery.error;

  return {
    error,
    isLoading,
    instances: filteredBmsQuery.data?.items ?? [],
    totalItems: totalBmsQuery.data?.total ?? 0,
  };
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

export const useBareMetalInstanceCatalogItems = (enabled = true) => {
  const client = useApiFetch(BareMetalInstanceCatalogItems);
  return useApiQuery({
    queryKey: apiQueryKey('v1/baremetal_instance_catalog_items'),
    queryFn: () => client.list({}),
    select: (data) => data.items,
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
