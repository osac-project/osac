import { useState } from 'react';
import { act, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  type ComputeInstance,
  ComputeInstanceConditionType,
  ComputeInstanceState,
  ConditionStatus,
} from '@osac/types';

import RestartRequiredBanner from './RestartRequiredBanner';
import { useRestartRequiredWatch } from './useRestartRequiredWatch';
import * as apiQuery from '../../api/use-api-query';
import { renderWithProviders } from '../../test-utils/TestProviders';

const baseVm = {
  id: 'vm-watch',
  status: { state: ComputeInstanceState.RUNNING, conditions: [] },
} as unknown as ComputeInstance;

const queryClient = (
  invalidateQueries: apiQuery.ApiQueryClient['invalidateQueries'],
): apiQuery.ApiQueryClient =>
  ({
    invalidateQueries,
    refetchQueries: vi.fn(),
    cancelQueries: vi.fn(),
    getQueryData: vi.fn(),
    setQueryData: vi.fn(),
    setQueriesData: vi.fn(),
  }) as apiQuery.ApiQueryClient;

describe('useRestartRequiredWatch', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('stops invalidating after eight ticks', async () => {
    vi.useFakeTimers();
    const invalidateQueries = vi
      .fn<apiQuery.ApiQueryClient['invalidateQueries']>()
      .mockResolvedValue(undefined);
    vi.spyOn(apiQuery, 'useApiQueryClient').mockReturnValue(queryClient(invalidateQueries));
    const onFinished = vi.fn();

    renderWithProviders(<WatchHarness onFinished={onFinished} />);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(16_000);
    });

    expect(invalidateQueries).toHaveBeenCalledTimes(8);
    expect(onFinished).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(4_000);
    });
    expect(invalidateQueries).toHaveBeenCalledTimes(8);
  });

  it('keeps the tick budget when the query client identity changes', async () => {
    vi.useFakeTimers();
    const invalidateQueries = vi
      .fn<apiQuery.ApiQueryClient['invalidateQueries']>()
      .mockResolvedValue(undefined);
    vi.spyOn(apiQuery, 'useApiQueryClient').mockImplementation(() =>
      queryClient(invalidateQueries),
    );

    renderWithProviders(<RerenderHarness />);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(14_000);
    });
    expect(invalidateQueries).toHaveBeenCalledTimes(7);

    fireEvent.click(screen.getByRole('button', { name: /^Rerender/ }));

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2_000);
    });
    expect(invalidateQueries).toHaveBeenCalledTimes(8);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(4_000);
    });
    expect(invalidateQueries).toHaveBeenCalledTimes(8);
  });

  it('shows the banner after a later condition arrives', async () => {
    const withCondition = {
      ...baseVm,
      status: {
        state: ComputeInstanceState.RUNNING,
        conditions: [
          {
            type: ComputeInstanceConditionType.RESTART_REQUIRED,
            status: ConditionStatus.TRUE,
          },
        ],
      },
    } as unknown as ComputeInstance;

    const Harness = () => {
      const [vm, setVm] = useState(baseVm);
      return (
        <>
          <RestartRequiredBanner vm={vm} />
          <button type="button" onClick={() => setVm(withCondition)}>
            Apply condition
          </button>
        </>
      );
    };

    const view = renderWithProviders(<Harness />);
    expect(screen.queryByText('Restart required')).not.toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Apply condition' }));
    expect(await screen.findByText('Restart required')).toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Warning alert details' }));
    expect(screen.getByText('Detected changes')).toBeInTheDocument();
  });
});

const WatchHarness = ({ onFinished }: { onFinished: () => void }) => {
  useRestartRequiredWatch({
    vmId: baseVm.id,
    enabled: true,
    onFinished,
  });
  return null;
};

const onRerenderFinished = vi.fn();

const RerenderHarness = () => {
  const [generation, setGeneration] = useState(0);
  useRestartRequiredWatch({
    vmId: baseVm.id,
    enabled: true,
    onFinished: onRerenderFinished,
  });
  return (
    <button type="button" onClick={() => setGeneration((value) => value + 1)}>
      Rerender {generation}
    </button>
  );
};
