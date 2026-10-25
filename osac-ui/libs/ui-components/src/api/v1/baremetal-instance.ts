import { type MessageInitShape } from '@bufbuild/protobuf';
import { useMutation } from '@tanstack/react-query';

import {
  BareMetalInstanceRunStrategy,
  BareMetalInstanceSchema,
  BareMetalInstances,
} from '@osac/types';
import { useInvalidateServiceQueries } from '@osac/ui-components/api/use-resource';

import { useApiFetch } from '../api-context';
import { buildUpdateMaskPaths } from './update-mask';

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
  const invalidateServiceQueries = useInvalidateServiceQueries();
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
    onSuccess: () => invalidateServiceQueries(BareMetalInstances),
  });
};

export const useDeleteBareMetalInstance = () => {
  const client = useApiFetch(BareMetalInstances);
  const invalidateServiceQueries = useInvalidateServiceQueries();
  return useMutation({
    mutationFn: (id: string) => client.delete({ id }),
    onSuccess: () => invalidateServiceQueries(BareMetalInstances),
  });
};

export const useCreateBareMetalInstance = () => {
  const client = useApiFetch(BareMetalInstances);
  const invalidateServiceQueries = useInvalidateServiceQueries();
  return useMutation({
    mutationFn: (bmi: MessageInitShape<typeof BareMetalInstanceSchema>) =>
      client.create({ object: bmi }).then((r) => r.object),
    onSuccess: () => invalidateServiceQueries(BareMetalInstances),
  });
};
