import { type MessageInitShape } from '@bufbuild/protobuf';
import { useMutation } from '@tanstack/react-query';

import {
  type StorageTier,
  StorageTierSchema,
  StorageTierSpecSchema,
  StorageTierState,
  StorageTiers,
} from '@osac/types/private';

import { useApiFetch } from '../../api-context';
import { cel } from '../../cel';
import { type ListParams, apiQueryKey } from '../../types';
import { type ApiQueryClient, useApiQuery, useApiQueryClient } from '../../use-api-query';
import { buildUpdateMaskPaths } from '../update-mask';

// Server-side CEL filter restricting the list to ACTIVE tiers. Enum fields compare
// with `==` against the int literal (see cluster-versions.ts for the enum caveat).
export const STORAGE_TIER_ACTIVE_LIST_FILTER = cel<StorageTier>((filter) =>
  filter.field('status.state').equals(StorageTierState.ACTIVE),
);

export const usePrivateStorageTiers = (params: ListParams = {}) => {
  const client = useApiFetch(StorageTiers);
  return useApiQuery({
    queryKey: apiQueryKey('v1/private/storage_tiers', undefined, params),
    queryFn: () => client.list(params),
    select: (data) => data.items,
  });
};

export const usePrivateStorageTier = (id: string) => {
  const client = useApiFetch(StorageTiers);
  return useApiQuery({
    queryKey: apiQueryKey('v1/private/storage_tiers', [id]),
    queryFn: () => client.get({ id }),
    select: (data) => data.object,
    enabled: Boolean(id),
  });
};

export const invalidateStorageTiersQueries = (qc: ApiQueryClient) =>
  qc.invalidateQueries({ queryKey: apiQueryKey('v1/private/storage_tiers') });

export const useCreateStorageTier = () => {
  const client = useApiFetch(StorageTiers);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: async (input: MessageInitShape<typeof StorageTierSchema>) => {
      const resp = await client.create({ object: input });
      if (!resp.object) {
        throw new Error('Create response missing object');
      }
      return resp.object;
    },
    onSuccess: () => invalidateStorageTiersQueries(qc),
  });
};

export type UpdateStorageTierInput = {
  id: string;
  metadata: { version: number };
  spec: MessageInitShape<typeof StorageTierSpecSchema>;
};

export const useUpdateStorageTier = () => {
  const client = useApiFetch(StorageTiers);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: async (input: UpdateStorageTierInput) => {
      const resp = await client.update({
        object: { id: input.id, metadata: input.metadata, spec: input.spec },
        updateMask: {
          paths: buildUpdateMaskPaths({ spec: input.spec }),
        },
        lock: true,
      });
      if (!resp.object) {
        throw new Error('Update response missing object');
      }
      return resp.object;
    },
    onSuccess: () => invalidateStorageTiersQueries(qc),
  });
};

export const useDeleteStorageTier = () => {
  const client = useApiFetch(StorageTiers);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: (id: string) => client.delete({ id }),
    onSuccess: () => invalidateStorageTiersQueries(qc),
  });
};
