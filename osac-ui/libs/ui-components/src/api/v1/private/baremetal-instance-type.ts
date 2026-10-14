import { type MessageInitShape } from '@bufbuild/protobuf';
import { useMutation } from '@tanstack/react-query';

import { BareMetalInstanceTypeSchema, BareMetalInstanceTypes } from '@osac/types/private';

import { useApiFetch } from '../../api-context';
import { type ListParams, apiQueryKey } from '../../types';
import { type ApiQueryClient, useApiQuery, useApiQueryClient } from '../../use-api-query';
import { buildUpdateMaskPaths } from '../update-mask';

export const useAdminBareMetalInstanceTypes = (params: ListParams = {}) => {
  const client = useApiFetch(BareMetalInstanceTypes);
  return useApiQuery({
    queryKey: apiQueryKey('v1/private/baremetal_instance_types', undefined, params),
    queryFn: () => client.list(params),
    select: (data) => data.items,
  });
};

export const invalidateBareMetalInstanceTypesQueries = (qc: ApiQueryClient) =>
  qc.invalidateQueries({ queryKey: apiQueryKey('v1/private/baremetal_instance_types') });

export type UpdateBareMetalInstanceTypeInput = {
  id: string;
  body: MessageInitShape<typeof BareMetalInstanceTypeSchema>;
};

export const useCreateBareMetalInstanceType = () => {
  const client = useApiFetch(BareMetalInstanceTypes);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: async (body: MessageInitShape<typeof BareMetalInstanceTypeSchema>) => {
      const resp = await client.create({ object: body });
      if (!resp.object) {
        throw new Error('Create response missing bare metal instance type object');
      }
      return resp.object;
    },
    onSuccess: () => invalidateBareMetalInstanceTypesQueries(qc),
  });
};

export const useUpdateBareMetalInstanceType = () => {
  const client = useApiFetch(BareMetalInstanceTypes);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: async ({ id, body }: UpdateBareMetalInstanceTypeInput) => {
      const resp = await client.update({
        object: { id, ...body },
        updateMask: {
          paths: buildUpdateMaskPaths(body, {
            schema: BareMetalInstanceTypeSchema,
          }),
        },
      });
      if (!resp.object) {
        throw new Error('Update response missing bare metal instance type object');
      }
      return resp.object;
    },
    onSuccess: () => invalidateBareMetalInstanceTypesQueries(qc),
  });
};

export const useDeleteBareMetalInstanceType = () => {
  const client = useApiFetch(BareMetalInstanceTypes);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: (id: string) => client.delete({ id }),
    onSuccess: () => invalidateBareMetalInstanceTypesQueries(qc),
  });
};
