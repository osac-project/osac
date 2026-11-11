import {
  Alert,
  Card,
  CardBody,
  CardTitle,
  Content,
  ProgressStepVariant,
  ProgressStepper,
  StackItem,
} from '@patternfly/react-core';

import { type Cluster, ClusterUpgradeState } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import ResourceProgressStep from '../../Resource/ResourceProgressStep';
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

  const source = attempt.sourceVersion || cluster.status?.observedVersion || '—';
  const target = attempt.targetVersion || '—';
  const title = t('Upgrade from {{source}} to {{target}}', { source, target });
  const progressVariant = succeeded
    ? ProgressStepVariant.success
    : progressing
      ? ProgressStepVariant.pending
      : ProgressStepVariant.default;
  let outcomeLabel = t('Success');
  let outcomeVariant = ProgressStepVariant.default;
  let outcomeDescription: string | undefined;
  if (succeeded) {
    outcomeVariant = ProgressStepVariant.success;
    outcomeDescription = t('The component reached the requested version.');
  } else if (failed) {
    outcomeLabel = t('Failed');
    outcomeVariant = ProgressStepVariant.danger;
    outcomeDescription = t('HyperShift reported a terminal error and stopped.');
  } else if (timedOut) {
    outcomeLabel = t('Timed out');
    outcomeVariant = ProgressStepVariant.warning;
    outcomeDescription = t('No definitive result within the timeout.');
  }

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
          <ProgressStepper isCenterAligned aria-label={t('Upgrade progress steps')}>
            <ResourceProgressStep
              stepId="cluster-upgrade-accepted"
              title={t('Request accepted')}
              stepStatus={{
                variant: ProgressStepVariant.success,
                isCurrent: pending,
                description: pending
                  ? t('Accepted by OSAC; HyperShift has not yet reported it started.')
                  : undefined,
              }}
            />
            <ResourceProgressStep
              stepId="cluster-upgrade-progressing"
              title={t('Progressing')}
              stepStatus={{
                variant: progressVariant,
                isCurrent: progressing,
                description: progressing
                  ? t('HyperShift is working toward the requested version.')
                  : undefined,
              }}
            />
            <ResourceProgressStep
              stepId="cluster-upgrade-outcome"
              title={outcomeLabel}
              stepStatus={{
                variant: outcomeVariant,
                isCurrent: failed || timedOut,
                description: outcomeDescription,
              }}
            />
          </ProgressStepper>
        </CardBody>
      </Card>
    </StackItem>
  );
};

export default ClusterUpgradeProgress;
