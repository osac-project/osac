import { type ComputeInstance, ComputeInstances } from '@osac/types';

import {
  INSTANCE_TYPE_UPDATE_MASK,
  instanceTypeUpdateSuccessTitle,
  instanceTypeUpdateWarningsDescription,
  isStoppedVm,
} from './instanceTypeUpdateToast';
import { useApiQueryClient } from '../../api/use-api-query';
import { useUpdateResource } from '../../api/use-resource';
import { invalidateComputeInstancesQueries } from '../../api/v1/compute-instance';
import { useTranslation } from '../../hooks/useTranslation';
import { useToast } from '../Toast/useToast';

export const useUpdateVmInstanceType = () => {
  const { t } = useTranslation();
  const { addToast } = useToast();
  const queryClient = useApiQueryClient();
  const updateVm = useUpdateResource(ComputeInstances);

  const updateInstanceType = async (
    vm: ComputeInstance,
    instanceTypeId: string,
    instanceTypeLabel: string,
  ): Promise<void> => {
    const response = await updateVm.mutateAsync({
      object: {
        id: vm.id,
        spec: { instanceType: { id: instanceTypeId } },
      },
      updateMask: { paths: [...INSTANCE_TYPE_UPDATE_MASK] },
    });

    addToast({
      variant: 'success',
      title: instanceTypeUpdateSuccessTitle(t, instanceTypeLabel, isStoppedVm(vm)),
      description: instanceTypeUpdateWarningsDescription(response.warnings) || undefined,
    });
    await invalidateComputeInstancesQueries(queryClient);
  };

  return { updateInstanceType };
};
