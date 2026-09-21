import type { TFunction } from 'i18next';

import { InstanceTypeState, InstanceTypes } from '@osac/types/private';
import { useUpdateResource } from '@osac/ui-components/api/use-resource';

import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import { getResourceLifecycleActions } from '../Resource/getResourceLifecycleActions';
import { useToast } from '../Toast/useToast';

export type InstanceTypeLifecycleAction = Exclude<InstanceTypeState, InstanceTypeState.UNSPECIFIED>;

const INSTANCE_TYPE_LIFECYCLE_ACTION_RULES = {
  canDeprecate: [
    InstanceTypeState.UNSPECIFIED,
    InstanceTypeState.ACTIVE,
    InstanceTypeState.OBSOLETE,
  ],
  canObsolete: [
    InstanceTypeState.UNSPECIFIED,
    InstanceTypeState.ACTIVE,
    InstanceTypeState.DEPRECATED,
  ],
  canReactivate: [
    InstanceTypeState.UNSPECIFIED,
    InstanceTypeState.DEPRECATED,
    InstanceTypeState.OBSOLETE,
  ],
  canDelete: [InstanceTypeState.OBSOLETE],
};

/** Determines which lifecycle transitions are valid from the instance type's current state. */
export const getInstanceTypeLifecycleActions = (state: InstanceTypeState | undefined) =>
  getResourceLifecycleActions(
    state,
    InstanceTypeState.UNSPECIFIED,
    INSTANCE_TYPE_LIFECYCLE_ACTION_RULES,
  );

const getLifecycleErrorTitle = (t: TFunction, action: InstanceTypeLifecycleAction): string => {
  switch (action) {
    case InstanceTypeState.DEPRECATED:
      return t('Failed to deprecate instance type');
    case InstanceTypeState.OBSOLETE:
      return t('Failed to mark instance type as obsolete');
    case InstanceTypeState.ACTIVE:
      return t('Failed to reactivate instance type');
  }
};

/** Runs an instance type lifecycle transition and surfaces a toast if it fails. */
export const useInstanceTypeLifecycleAction = () => {
  const { t } = useTranslation();
  const { addToast } = useToast();
  const updateInstanceType = useUpdateResource(InstanceTypes);

  const runLifecycleAction = (instanceTypeId: string, action: InstanceTypeLifecycleAction) => {
    updateInstanceType.mutate(
      { object: { id: instanceTypeId, spec: { state: action } } },
      {
        onError: (error) => {
          addToast({
            variant: 'danger',
            title: getLifecycleErrorTitle(t, action),
            description: getErrorMessage(error),
          });
        },
      },
    );
  };

  return { runLifecycleAction };
};
