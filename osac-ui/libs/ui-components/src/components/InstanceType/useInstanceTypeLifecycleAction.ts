import type { TFunction } from 'i18next';

import { InstanceTypeState, InstanceTypes } from '@osac/types/private';
import { useUpdateResource } from '@osac/ui-components/api/use-resource';

import {
  type InstanceTypeLifecycleMenuAction,
  type InstanceTypeLifecycleState,
  instanceTypeLifecycleActionsByState,
} from './instanceTypeLifecycle';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import {
  type ResourceLifecycleActionRules,
  type ResourceLifecycleActions,
  getResourceLifecycleActions,
} from '../Resource/getResourceLifecycleActions';
import { useToast } from '../Toast/useToast';

export type InstanceTypeLifecycleAction = Exclude<InstanceTypeState, InstanceTypeState.UNSPECIFIED>;

const stateValueByLabel: Record<InstanceTypeLifecycleState, InstanceTypeState> = {
  Unspecified: InstanceTypeState.UNSPECIFIED,
  Active: InstanceTypeState.ACTIVE,
  Deprecated: InstanceTypeState.DEPRECATED,
  Obsolete: InstanceTypeState.OBSOLETE,
};

const statesForAction = (action: InstanceTypeLifecycleMenuAction): InstanceTypeState[] =>
  (Object.keys(instanceTypeLifecycleActionsByState) as InstanceTypeLifecycleState[])
    .filter((state) => instanceTypeLifecycleActionsByState[state].includes(action))
    .map((state) => stateValueByLabel[state]);

const instanceTypeLifecycleActionRules: ResourceLifecycleActionRules<InstanceTypeState> = {
  canDeprecate: statesForAction('Deprecate'),
  canObsolete: statesForAction('Obsolete'),
  canReactivate: statesForAction('Reactivate'),
  canDelete: statesForAction('Delete'),
};

const stateLabel = (
  state: InstanceTypeState | undefined,
): InstanceTypeLifecycleState | undefined => {
  switch (state) {
    case undefined:
    case InstanceTypeState.UNSPECIFIED:
      return 'Unspecified';
    case InstanceTypeState.ACTIVE:
      return 'Active';
    case InstanceTypeState.DEPRECATED:
      return 'Deprecated';
    case InstanceTypeState.OBSOLETE:
      return 'Obsolete';
    default:
      return undefined;
  }
};

const noLifecycleActions: ResourceLifecycleActions = {
  canDeprecate: false,
  canObsolete: false,
  canReactivate: false,
  canDelete: false,
};

/** Determines which lifecycle transitions are valid from the instance type's current state. */
export const getInstanceTypeLifecycleActions = (state: InstanceTypeState | undefined) => {
  const label = stateLabel(state);
  if (label === undefined) {
    return noLifecycleActions;
  }

  return getResourceLifecycleActions(
    stateValueByLabel[label],
    InstanceTypeState.UNSPECIFIED,
    instanceTypeLifecycleActionRules,
  );
};

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
