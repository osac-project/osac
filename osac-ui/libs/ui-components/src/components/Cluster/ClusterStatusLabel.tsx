import { Content, Label, Stack, StackItem } from '@patternfly/react-core';
import ExclamationTriangleIcon from '@patternfly/react-icons/dist/esm/icons/exclamation-triangle-icon';
import type { TFunction } from 'i18next';

import { ClusterConditionType, ClusterState, type ClusterStatus } from '@osac/types';

import { useTranslation } from '../../hooks/useTranslation';
import { ResourceStatusLabel, type StatusKind } from '../Resource/ResourceStatusLabel';

interface ClusterStatusLabelProps {
  status?: ClusterStatus;
  activityInLabel?: boolean;
}

interface ClusterStateActivity {
  stateKind: StatusKind;
  stateText: string;
  activityText?: string;
  activityWarning: boolean;
}

interface ClusterStatusInfo {
  status: StatusKind;
  text: (t: TFunction) => string;
}

const UNKNOWN_CLUSTER_STATUS: ClusterStatusInfo = {
  status: 'unspecified',
  text: (t) => t('Unknown'),
};

const CLUSTER_STATUS_MAP = new Map<ClusterState, ClusterStatusInfo>([
  [ClusterState.UNSPECIFIED, UNKNOWN_CLUSTER_STATUS],
  [ClusterState.PROGRESSING, { status: 'progressing', text: (t) => t('Provisioning') }],
  [ClusterState.READY, { status: 'ready', text: (t) => t('Ready') }],
  [ClusterState.FAILED, { status: 'failed', text: (t) => t('Failed') }],
  [ClusterState.DELETING, { status: 'progressing', text: (t) => t('Deleting') }],
  [ClusterState.DELETE_FAILED, { status: 'failed', text: (t) => t('Delete failed') }],
]);

const resolveClusterState = (
  state: ClusterState | undefined,
  t: TFunction,
): Pick<ClusterStateActivity, 'stateKind' | 'stateText'> => {
  const { status, text } =
    CLUSTER_STATUS_MAP.get(state ?? ClusterState.UNSPECIFIED) ?? UNKNOWN_CLUSTER_STATUS;
  return { stateKind: status, stateText: text(t) };
};

const formatClusterStateActivity = (
  status: ClusterStatus | undefined,
  t: TFunction,
): ClusterStateActivity => {
  const state = status?.state;
  const stateInfo = resolveClusterState(state, t);
  if (!status || state !== ClusterState.PROGRESSING) {
    return { ...stateInfo, activityWarning: false };
  }

  const reason = status.conditions.find(
    (condition) => condition.type === ClusterConditionType.PROGRESSING,
  )?.reason;
  let activityText: string;
  if (!reason?.trim() || reason === 'StageUnknown') {
    activityText = t('Stage unknown');
  } else if (reason === 'Stalled') {
    activityText = t('Stalled');
  } else {
    activityText = reason;
  }
  return {
    ...stateInfo,
    activityText,
    activityWarning: reason === 'Stalled',
  };
};

export const ClusterStatusLabel = ({
  status,
  activityInLabel = false,
}: ClusterStatusLabelProps) => {
  const { t } = useTranslation();
  const { stateKind, stateText, activityText, activityWarning } = formatClusterStateActivity(
    status,
    t,
  );
  const stateLabel = <ResourceStatusLabel status={stateKind} text={stateText} />;

  if (!activityText) {
    return stateLabel;
  }

  if (activityInLabel) {
    const text = t('Provisioning - {{activity}}', { activity: activityText });
    return activityWarning ? (
      <Label
        color="orange"
        icon={<ExclamationTriangleIcon aria-hidden />}
        aria-label={t('Warning: Provisioning stalled')}
        isCompact
      >
        {text}
      </Label>
    ) : (
      <ResourceStatusLabel status={stateKind} text={text} />
    );
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
