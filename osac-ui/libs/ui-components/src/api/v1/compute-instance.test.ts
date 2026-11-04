import type { QueryClient } from '@tanstack/react-query';
import { describe, expect, it, vi } from 'vitest';

import { ComputeInstanceState, ComputeInstances } from '@osac/types';

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
    const getQueriesData = vi
      .fn()
      .mockReturnValue([
        [['ignored', 'list', {}], { items: [makeVm('vm-1', ComputeInstanceState.RUNNING)] }],
      ]);
    const queryClient = {
      invalidateQueries,
      getQueriesData,
    } as unknown as QueryClient;

    await pollComputeInstancesUntilListed(queryClient, 'vm-1');

    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: [ComputeInstances.typeName],
    });
    expect(getQueriesData).toHaveBeenCalledWith({
      queryKey: [ComputeInstances.typeName, 'list'],
    });
  });
});
