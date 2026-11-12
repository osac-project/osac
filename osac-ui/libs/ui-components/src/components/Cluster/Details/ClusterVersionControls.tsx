import { useId, useState } from 'react';
import { Button, Content, Flex, FlexItem, Label } from '@patternfly/react-core';

import { type Cluster, ClusterState, ClusterUpgradeState, ClusterVersions } from '@osac/types';

import {
  type ClusterUpgradeChoiceResult,
  resolveClusterUpgradeChoices,
} from './clusterUpgradeChoices';
import UpgradeClusterDialog from './UpgradeClusterDialog';
import { useGetResource, useListAllResources } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import {
  getActiveControlPlaneUpgradeTarget,
  getControlPlaneUpgradeHistory,
} from '../ListPage/clusterDisplay';

interface ClusterVersionControlsProps {
  cluster: Cluster;
}

type UpgradeActionState =
  | { kind: 'hidden' }
  | { kind: 'loading' }
  | { kind: 'available' }
  | { kind: 'in-progress' }
  | { kind: 'blocked'; reason: string };

const countCurrentWorkerNodes = (cluster: Cluster): number => {
  const statusNodeSets = cluster.status?.nodeSets ?? {};
  const specNodeSets = cluster.spec?.nodeSets ?? {};
  const nodeSetIds = new Set([...Object.keys(statusNodeSets), ...Object.keys(specNodeSets)]);
  let count = 0;
  for (const id of nodeSetIds) {
    count += statusNodeSets[id]?.size ?? specNodeSets[id]?.size ?? 0;
  }
  return count;
};

const ClusterVersionControls = ({ cluster }: ClusterVersionControlsProps) => {
  const { t } = useTranslation();
  const reasonId = useId();
  const [openClusterId, setOpenClusterId] = useState<string>();
  const dialogOpen = openClusterId === cluster.id;
  const configuredVersionId = cluster.spec?.version?.id;
  const hideUpgradeControls =
    cluster.status?.state === ClusterState.FAILED ||
    cluster.status?.state === ClusterState.DELETING ||
    cluster.status?.state === ClusterState.DELETE_FAILED;
  const latestUpgrade = cluster.status?.upgradeHistory[0];
  const latestControlPlaneUpgrade = getControlPlaneUpgradeHistory(cluster)[0];
  const observedVersion = cluster.status?.observedVersion;
  const activeTarget = getActiveControlPlaneUpgradeTarget(cluster);
  const upgradePending = latestControlPlaneUpgrade?.state === ClusterUpgradeState.PENDING;
  const upgradeProgressing = latestControlPlaneUpgrade?.state === ClusterUpgradeState.PROGRESSING;
  const upgradeFailed = latestControlPlaneUpgrade?.state === ClusterUpgradeState.FAILED;
  const upgradeTimedOut = latestControlPlaneUpgrade?.state === ClusterUpgradeState.TIMED_OUT;
  const upgradeInProgress = upgradePending || upgradeProgressing;
  const latestAttemptInProgress =
    latestUpgrade?.state === ClusterUpgradeState.PENDING ||
    latestUpgrade?.state === ClusterUpgradeState.PROGRESSING;
  const upgradeDidNotComplete =
    latestUpgrade?.state === ClusterUpgradeState.TIMED_OUT ||
    latestUpgrade?.state === ClusterUpgradeState.FAILED;
  const configuredVersionQuery = useGetResource(
    ClusterVersions,
    { id: configuredVersionId ?? '' },
    { enabled: Boolean(configuredVersionId) && !hideUpgradeControls },
  );
  const catalogQuery = useListAllResources(ClusterVersions, undefined, {
    enabled: Boolean(configuredVersionId) && !hideUpgradeControls,
  });
  const configuredVersion = configuredVersionQuery.data?.object;

  let choices: ClusterUpgradeChoiceResult = { eligibleTargets: [] };
  let action: UpgradeActionState;
  if (hideUpgradeControls) {
    action = { kind: 'hidden' };
  } else if (upgradeInProgress || latestAttemptInProgress) {
    action = { kind: 'in-progress' };
  } else if (upgradeFailed || upgradeTimedOut || upgradeDidNotComplete) {
    action = {
      kind: 'blocked',
      reason: latestUpgrade?.message || t('The latest upgrade did not complete.'),
    };
  } else if (!configuredVersionId) {
    action = { kind: 'blocked', reason: t('Upgrade eligibility is unavailable.') };
  } else if (
    configuredVersionQuery.isLoading ||
    catalogQuery.isLoading ||
    catalogQuery.isPlaceholderData
  ) {
    action = { kind: 'loading' };
  } else if (configuredVersionQuery.isError || catalogQuery.isError) {
    action = { kind: 'blocked', reason: t('Could not load upgrade options.') };
  } else {
    choices = resolveClusterUpgradeChoices(configuredVersion, catalogQuery.data?.items ?? [], t);
    action = choices.blockedReason
      ? { kind: 'blocked', reason: choices.blockedReason }
      : { kind: 'available' };
  }

  let upgradeLabel;
  if (upgradePending) {
    upgradeLabel = <Label color="blue">{t('Upgrade pending')}</Label>;
  } else if (upgradeProgressing) {
    upgradeLabel = <Label color="blue">{t('Upgrade in progress')}</Label>;
  } else if (upgradeFailed) {
    upgradeLabel = <Label status="danger">{t('Upgrade failed')}</Label>;
  } else if (upgradeTimedOut) {
    upgradeLabel = <Label status="warning">{t('Upgrade timed out')}</Label>;
  } else if (action.kind === 'in-progress') {
    upgradeLabel = <Label color="blue">{t('Upgrade in progress')}</Label>;
  } else if (action.kind === 'available') {
    upgradeLabel = <Label color="orange">{t('Upgrade available')}</Label>;
  } else if (action.kind === 'blocked') {
    upgradeLabel = <Label color="grey">{t('No upgrade available')}</Label>;
  }

  let explanation: string | undefined;
  if (upgradeFailed) {
    explanation =
      latestControlPlaneUpgrade?.message || t('HyperShift reported a terminal error and stopped.');
  } else if (upgradeTimedOut) {
    explanation =
      latestControlPlaneUpgrade?.message || t('No definitive result within the timeout.');
  } else if (upgradeInProgress) {
    explanation = latestControlPlaneUpgrade?.message || undefined;
  } else if (action.kind === 'blocked') {
    explanation = action.reason;
  } else if (action.kind === 'loading') {
    explanation = t('Checking upgrade options');
  } else if (action.kind === 'in-progress') {
    explanation = latestUpgrade?.message || undefined;
  }

  return (
    <>
      <Flex
        alignItems={{ default: 'alignItemsCenter' }}
        spaceItems={{ default: 'spaceItemsSm' }}
        flexWrap={{ default: 'wrap' }}
      >
        <FlexItem>
          {observedVersion || '—'}
          {upgradeInProgress && observedVersion && activeTarget ? ` → ${activeTarget}` : null}
        </FlexItem>
        {upgradeLabel && <FlexItem>{upgradeLabel}</FlexItem>}
        {action.kind !== 'hidden' && action.kind !== 'in-progress' && (
          <FlexItem>
            <Button
              variant="link"
              isInline
              onClick={() => setOpenClusterId(cluster.id)}
              isDisabled={action.kind !== 'available'}
              aria-describedby={explanation ? reasonId : undefined}
              aria-busy={action.kind === 'loading' || undefined}
            >
              {t('Upgrade cluster')}
            </Button>
          </FlexItem>
        )}
      </Flex>
      {explanation && (
        <Content component="p" id={reasonId}>
          {explanation}
        </Content>
      )}
      {dialogOpen && (
        <UpgradeClusterDialog
          clusterId={cluster.id}
          currentVersion={cluster.status?.observedVersion}
          eligibleVersions={choices.blockedReason ? [] : choices.eligibleTargets}
          workerNodeCount={countCurrentWorkerNodes(cluster)}
          onClose={() => setOpenClusterId(undefined)}
        />
      )}
    </>
  );
};

export default ClusterVersionControls;
