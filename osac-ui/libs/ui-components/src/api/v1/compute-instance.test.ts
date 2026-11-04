import { describe, expect, it, vi } from 'vitest';

import { ComputeInstanceState } from '@osac/types';

import { pollComputeInstancesUntilListed } from './compute-instance';

const makeVm = (id: string, state: ComputeInstanceState) => ({
  id,
  status: { state },
  metadata: { name: `vm-${id}` },
  spec: {},
});

describe('pollComputeInstancesUntilListed', () => {
  it('checks the list cache after invalidation', async () => {
    const invalidateQueries = vi.fn().mockResolvedValue(undefined);
    const getQueryData = vi.fn().mockReturnValue({
      items: [makeVm('vm-1', ComputeInstanceState.RUNNING)],
    });
    const queryClient = {
      invalidateQueries,
      getQueryData,
    } as unknown as Parameters<typeof pollComputeInstancesUntilListed>[0];

    await pollComputeInstancesUntilListed(queryClient, 'vm-1');

    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: ['v1/compute_instances'],
    });
    expect(getQueryData).toHaveBeenCalledWith(['v1/compute_instances']);
  });
});
