import { type ComputeInstance, ComputeInstances } from '@osac/types';

import {
  INSTANCE_TYPE_UPDATE_MASK,
  instanceTypeUpdateSuccessTitle,
  instanceTypeUpdateWarningsDescription,
  isStoppedVm,
} from './instanceTypeUpdateToast';
import { useUpdateResource } from '../../api/use-resource';
import { useTranslation } from '../../hooks/useTranslation';
import { useToast } from '../Toast/useToast';

export const useUpdateVmInstanceType = () => {
  const { t } = useTranslation();
  const { addToast } = useToast();
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
  };

  return { updateInstanceType };
};
