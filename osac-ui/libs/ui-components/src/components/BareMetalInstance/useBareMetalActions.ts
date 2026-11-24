import type { MessageInitShape } from '@bufbuild/protobuf';
import type { TFunction } from 'i18next';

import {
  type BareMetalInstance,
  BareMetalInstanceRunStrategy,
  BareMetalInstanceSchema,
  BareMetalInstanceState,
  BareMetalInstances,
} from '@osac/types';

import { useUpdateResource } from '../../api/use-resource';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import { useToast } from '../Toast/useToast';

const getBmActionSuccessTitle = (t: TFunction, action: BareMetalPowerAction, name: string) => {
  switch (action) {
    case 'start':
      return t('Start initiated for {{name}}', { name });
    case 'stop':
      return t('Stop initiated for {{name}}', { name });
    case 'restart':
      return t('Restart initiated for {{name}}', { name });
  }
};

const getBmActionErrorTitle = (t: TFunction, action: BareMetalPowerAction, name: string) => {
  switch (action) {
    case 'start':
      return t('Failed to start bare metal instance {{name}}', { name });
    case 'stop':
      return t('Failed to stop bare metal instance {{name}}', { name });
    case 'restart':
      return t('Failed to restart bare metal instance {{name}}', { name });
  }
};

type BareMetalPowerAction = 'start' | 'stop' | 'restart';

type PatchBareMetalInstanceInput =
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

export const useBareMetalActions = (instance: BareMetalInstance) => {
  const { t } = useTranslation();
  const { addToast } = useToast();
  const patch = useUpdateResource(BareMetalInstances);

  const name = instance.metadata?.name ?? instance.id;
  const state = instance.status?.state;
  const canStart = state === BareMetalInstanceState.STOPPED;
  const canStop = state === BareMetalInstanceState.RUNNING;
  const canRestart = state === BareMetalInstanceState.RUNNING;
  const canDelete = state !== BareMetalInstanceState.DELETING;

  const mutateWithToast = (input: PatchBareMetalInstanceInput) => {
    const body = buildPatchBody(input);
    patch.mutate(
      { object: { id: input.id, ...body } },
      {
        onSuccess: () => {
          addToast({
            variant: 'success',
            title: getBmActionSuccessTitle(t, input.action, name),
          });
        },
        onError: (error) => {
          addToast({
            variant: 'danger',
            title: getBmActionErrorTitle(t, input.action, name),
            description: getErrorMessage(error),
          });
        },
      },
    );
  };

  const start = () => {
    if (canStart) {
      mutateWithToast({ id: instance.id, action: 'start' });
    }
  };

  const stop = () => {
    if (canStop) {
      mutateWithToast({ id: instance.id, action: 'stop' });
    }
  };

  const restart = () => {
    if (canRestart) {
      mutateWithToast({
        id: instance.id,
        action: 'restart',
        currentTrigger: instance.spec?.restartTrigger ?? 0n,
      });
    }
  };

  return { canStart, canStop, canRestart, canDelete, start, stop, restart };
};
