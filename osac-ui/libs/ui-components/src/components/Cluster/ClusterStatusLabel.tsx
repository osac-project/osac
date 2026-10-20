import { Content, Label, Stack, StackItem } from '@patternfly/react-core';
import ExclamationTriangleIcon from '@patternfly/react-icons/dist/esm/icons/exclamation-triangle-icon';
import type { TFunction } from 'i18next';

import { ClusterConditionType, ClusterState, type ClusterStatus } from '@osac/types';

import { useTranslation } from '../../hooks/useTranslation';
import { ResourceStatusLabel, type StatusKind } from '../Resource/ResourceStatusLabel';

interface ClusterStatusLabelProps {
  status?: ClusterStatus;
}

interface ClusterStateActivity {
  stateKind: StatusKind;
  stateText: string;
  activityText?: string;
  activityWarning: boolean;
}

const resolveClusterState = (
  state: ClusterState | undefined,
  t: TFunction,
): Pick<ClusterStateActivity, 'stateKind' | 'stateText'> => {
  switch (state) {
    case ClusterState.PROGRESSING:
      return { stateKind: 'progressing', stateText: t('Provisioning') };
    case ClusterState.READY:
      return { stateKind: 'ready', stateText: t('Ready') };
    case ClusterState.FAILED:
      return { stateKind: 'failed', stateText: t('Failed') };
    case ClusterState.DELETING:
      return { stateKind: 'progressing', stateText: t('Deleting') };
    case ClusterState.DELETE_FAILED:
      return { stateKind: 'failed', stateText: t('Delete failed') };
    default:
      return { stateKind: 'unspecified', stateText: t('Unknown') };
  }
};

const formatClusterStateActivity = (
  status: ClusterStatus | undefined,
  t: TFunction,
): ClusterStateActivity => {
  const state = status?.state;
  const stateInfo = resolveClusterState(state, t);
  if (status?.state !== ClusterState.PROGRESSING) {
    return { ...stateInfo, activityWarning: false };
  }

  const reason = status.conditions.find(
    (condition) => condition.type === ClusterConditionType.PROGRESSING,
  )?.reason;
  return {
    ...stateInfo,
    activityText:
      !reason?.trim() || reason === 'StageUnknown'
        ? t('Stage unknown')
        : reason === 'Stalled'
          ? t('Stalled')
          : reason,
    activityWarning: reason === 'Stalled',
  };
};

export const ClusterStatusLabel = ({ status }: ClusterStatusLabelProps) => {
  const { t } = useTranslation();
  const { stateKind, stateText, activityText, activityWarning } = formatClusterStateActivity(
    status,
    t,
  );
  const stateLabel = <ResourceStatusLabel status={stateKind} text={stateText} />;

  if (!activityText) {
    return stateLabel;
  }

  return (
    <Stack>
      <StackItem>{stateLabel}</StackItem>
      <StackItem>
        {activityWarning ? (
          <Label
            color="orange"
            icon={<ExclamationTriangleIcon aria-hidden />}
            aria-label={t('Warning: Stalled')}
            isCompact
          >
            {activityText}
          </Label>
        ) : (
          <Content component="small">{activityText}</Content>
        )}
      </StackItem>
    </Stack>
  );
};
