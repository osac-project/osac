import { useEffect, useRef } from 'react';

import { apiQueryKey } from '../../api/types';
import { useApiQueryClient } from '../../api/use-api-query';

const RESTART_REQUIRED_WATCH_INTERVAL_MS = 2000;
const RESTART_REQUIRED_WATCH_MAX_TICKS = 8;

export const useRestartRequiredWatch = ({
  vmId,
  enabled,
  onFinished,
}: {
  vmId: string;
  enabled: boolean;
  onFinished: () => void;
}) => {
  const queryClient = useApiQueryClient();
  const queryClientRef = useRef(queryClient);
  const onFinishedRef = useRef(onFinished);
  queryClientRef.current = queryClient;
  onFinishedRef.current = onFinished;

  useEffect(() => {
    if (!enabled || !vmId) {
      return undefined;
    }

    let ticks = 0;
    let timer = 0;
    const tick = () => {
      ticks += 1;
      void queryClientRef.current.invalidateQueries({
        queryKey: apiQueryKey('v1/compute_instances', [vmId]),
      });
      if (ticks >= RESTART_REQUIRED_WATCH_MAX_TICKS) {
        window.clearInterval(timer);
        onFinishedRef.current();
      }
    };

    timer = window.setInterval(tick, RESTART_REQUIRED_WATCH_INTERVAL_MS);
    return () => window.clearInterval(timer);
  }, [enabled, vmId]);
};
