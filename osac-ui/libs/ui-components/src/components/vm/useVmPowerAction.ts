import { type MessageInitShape } from '@bufbuild/protobuf';
import { timestampNow } from '@bufbuild/protobuf/wkt';

import { ComputeInstanceRunStrategy, ComputeInstanceSchema, ComputeInstances } from '@osac/types';

import {
  type ComputeInstancePowerAction,
  getPowerActionErrorTitle,
  getPowerActionSuccessTitle,
} from './powerActionErrorTitle';
import { useUpdateResource } from '../../api/use-resource';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import { useToast } from '../Toast/useToast';

const buildPowerActionBody = (
  powerAction: ComputeInstancePowerAction,
): MessageInitShape<typeof ComputeInstanceSchema> => {
  switch (powerAction) {
    case 'stop':
      return {
        spec: { runStrategy: ComputeInstanceRunStrategy.COMPUTE_INSTANCE_RUN_STRATEGY_HALTED },
      };
    case 'start':
      return {
        spec: { runStrategy: ComputeInstanceRunStrategy.COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS },
      };
    case 'restart':
      return { spec: { restartRequestedAt: timestampNow() } };
  }
};

/** Runs a VM lifecycle power action and surfaces a toast on success or failure. */
export const useVmPowerAction = () => {
  const { t } = useTranslation();
  const { addToast } = useToast();
  const updateVm = useUpdateResource(ComputeInstances);

  const runPowerAction = (
    vmId: string,
    vmName: string,
    powerAction: ComputeInstancePowerAction,
  ) => {
    const body = buildPowerActionBody(powerAction);
    updateVm.mutate(
      {
        object: { id: vmId, ...body },
      },
      {
        onSuccess: () => {
          addToast({
            variant: 'success',
            title: getPowerActionSuccessTitle(t, powerAction, vmName),
          });
        },
        onError: (error) => {
          addToast({
            variant: 'danger',
            title: getPowerActionErrorTitle(t, powerAction, vmName),
            description: getErrorMessage(error),
          });
        },
      },
    );
  };

  return { runPowerAction };
};
