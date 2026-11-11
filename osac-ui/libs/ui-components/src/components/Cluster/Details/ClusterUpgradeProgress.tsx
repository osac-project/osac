import {
  Alert,
  Card,
  CardBody,
  CardTitle,
  Content,
  ProgressStep,
  ProgressStepVariant,
  ProgressStepper,
  StackItem,
} from '@patternfly/react-core';
import { RhUiInProgressIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-in-progress-icon';

import { type Cluster, ClusterUpgradeState } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { getControlPlaneUpgradeHistory } from '../ListPage/clusterDisplay';

interface ClusterUpgradeProgressProps {
  cluster: Cluster;
}

const ClusterUpgradeProgress = ({ cluster }: ClusterUpgradeProgressProps) => {
  const { t } = useTranslation();
  const attempt = getControlPlaneUpgradeHistory(cluster)[0];
  if (!attempt) {
    return null;
  }

  const pending = attempt.state === ClusterUpgradeState.PENDING;
  const progressing = attempt.state === ClusterUpgradeState.PROGRESSING;
  const succeeded = attempt.state === ClusterUpgradeState.SUCCEEDED;
  const timedOut = attempt.state === ClusterUpgradeState.TIMED_OUT;
  const failed = attempt.state === ClusterUpgradeState.FAILED;
  if (!pending && !progressing && !succeeded && !timedOut && !failed) {
    return null;
  }

  const source = attempt.sourceVersion.trim() || cluster.status?.observedVersion || '—';
  const target = attempt.targetVersion.trim() || '—';
  const title =
    pending || progressing
      ? t('Upgrading from {{source}} to {{target}}', { source, target })
      : t('Upgrade from {{source}} to {{target}}', { source, target });
  const progressVariant = succeeded
    ? ProgressStepVariant.success
    : progressing
      ? ProgressStepVariant.pending
      : ProgressStepVariant.default;
  const progressStatus = succeeded ? t('Complete') : progressing ? t('In progress') : undefined;
  const outcomeLabel = timedOut ? t('Timed out') : failed ? t('Failed') : t('Success');
  const outcomeVariant = timedOut
    ? ProgressStepVariant.warning
    : failed
      ? ProgressStepVariant.danger
      : succeeded
        ? ProgressStepVariant.success
        : ProgressStepVariant.default;

  return (
    <StackItem>
      <Card aria-label={t('Upgrade progress')}>
        <CardTitle component="h2">{title}</CardTitle>
        <CardBody>
          {(timedOut || failed) && (
            <Alert
              variant={timedOut ? 'warning' : 'danger'}
              isInline
              title={timedOut ? t('Upgrade timed out') : t('Upgrade failed')}
            >
              {attempt.message}
            </Alert>
          )}
          {(pending || progressing || succeeded) && attempt.message && (
            <Content component="p">{attempt.message}</Content>
          )}
          <ProgressStepper aria-label={t('Upgrade progress steps')}>
            <ProgressStep
              id="cluster-upgrade-accepted"
              titleId="cluster-upgrade-accepted-title"
              variant={ProgressStepVariant.success}
              isCurrent={pending}
              aria-label={`${t('Request accepted')}, ${t('Complete')}`}
            >
              {t('Request accepted')}
            </ProgressStep>
            <ProgressStep
              id="cluster-upgrade-progressing"
              titleId="cluster-upgrade-progressing-title"
              variant={progressVariant}
              isCurrent={progressing}
              icon={progressing ? <RhUiInProgressIcon /> : undefined}
              aria-label={
                progressStatus ? `${t('Progressing')}, ${progressStatus}` : t('Progressing')
              }
            >
              {t('Progressing')}
            </ProgressStep>
            <ProgressStep
              id="cluster-upgrade-outcome"
              titleId="cluster-upgrade-outcome-title"
              variant={outcomeVariant}
              isCurrent={failed || timedOut}
              aria-label={succeeded ? `${outcomeLabel}, ${t('Complete')}` : outcomeLabel}
            >
              {outcomeLabel}
            </ProgressStep>
          </ProgressStepper>
        </CardBody>
      </Card>
    </StackItem>
  );
};

export default ClusterUpgradeProgress;
