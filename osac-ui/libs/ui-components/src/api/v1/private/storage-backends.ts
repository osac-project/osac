import { type MessageInitShape, create } from '@bufbuild/protobuf';
import { useMutation } from '@tanstack/react-query';

import {
  type StorageBackend,
  StorageBackendCredentialsSchema,
  StorageBackendSchema,
  StorageBackendSpecSchema,
  StorageBackendState,
  StorageBackends,
} from '@osac/types/private';

import { useApiFetch } from '../../api-context';
import { cel } from '../../cel';
import { type ListParams, apiQueryKey } from '../../types';
import { type ApiQueryClient, useApiQuery, useApiQueryClient } from '../../use-api-query';
import { buildUpdateMaskPaths } from '../update-mask';

export const STORAGE_BACKEND_READY_LIST_FILTER = cel<StorageBackend>((filter) =>
  filter.field('status.state').equals(StorageBackendState.READY),
);

export const storageBackendIdsFilter = (ids: string[]) =>
  cel<StorageBackend>((filter) => filter.field('id').isIn(ids));

type StorageBackendsListOptions = {
  enabled?: boolean;
};

export const usePrivateStorageBackends = (
  params: ListParams = {},
  options: StorageBackendsListOptions = {},
) => {
  const client = useApiFetch(StorageBackends);
  return useApiQuery({
    queryKey: apiQueryKey('v1/private/storage_backends', undefined, params),
    queryFn: () => client.list(params),
    select: (data) => data.items,
    enabled: options.enabled ?? true,
  });
};

export const usePrivateStorageBackend = (id: string) => {
  const client = useApiFetch(StorageBackends);
  return useApiQuery({
    queryKey: apiQueryKey('v1/private/storage_backends', [id]),
    queryFn: () => client.get({ id }),
    select: (data) => data.object,
    enabled: Boolean(id),
  });
};

export const invalidateStorageBackendsQueries = (qc: ApiQueryClient) =>
  qc.invalidateQueries({ queryKey: apiQueryKey('v1/private/storage_backends') });

export const useCreateStorageBackend = () => {
  const client = useApiFetch(StorageBackends);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: async (input: MessageInitShape<typeof StorageBackendSchema>) => {
      const resp = await client.create({ object: input });
      if (!resp.object) {
        throw new Error('Create response missing object');
      }
      return resp.object;
    },
    onSuccess: () => invalidateStorageBackendsQueries(qc),
  });
};

export type UpdateStorageBackendInput = {
  id: string;
  /** The `metadata.version` of the record the caller last fetched — required so the server can enforce the lock below; a request with no metadata is exempt from the optimistic-lock check regardless of `lock: true`. */
  version: number;
  spec: MessageInitShape<typeof StorageBackendSpecSchema>;
};

export const useUpdateStorageBackend = () => {
  const client = useApiFetch(StorageBackends);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: async (input: UpdateStorageBackendInput) => {
      const spec: MessageInitShape<typeof StorageBackendSpecSchema> = { ...input.spec };
      if (spec.credentials) {
        spec.credentials = create(StorageBackendCredentialsSchema, spec.credentials);
      }

      const resp = await client.update({
        object: { id: input.id, metadata: { version: input.version }, spec },
        updateMask: { paths: buildUpdateMaskPaths({ spec }) },
        lock: true,
      });
      if (!resp.object) {
        throw new Error('Update response missing object');
      }
      return resp.object;
    },
    onSuccess: () => invalidateStorageBackendsQueries(qc),
  });
};

export const useDeleteStorageBackend = () => {
  const client = useApiFetch(StorageBackends);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: (id: string) => client.delete({ id }),
    onSuccess: () => invalidateStorageBackendsQueries(qc),
  });
};
