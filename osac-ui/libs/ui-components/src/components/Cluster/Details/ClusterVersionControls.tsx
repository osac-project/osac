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
import { getActiveControlPlaneUpgradeTarget } from '../ListPage/clusterDisplay';

interface ClusterVersionControlsProps {
  cluster: Cluster;
}

type UpgradeActionState =
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
  const observedVersion = cluster.status?.observedVersion?.trim();
  const activeTarget = getActiveControlPlaneUpgradeTarget(cluster);
  const upgradeInProgress =
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

  if (hideUpgradeControls) {
    return <>{cluster.status?.observedVersion || '—'}</>;
  }

  let choices: ClusterUpgradeChoiceResult = { eligibleTargets: [] };
  let action: UpgradeActionState;
  if (upgradeInProgress) {
    action = { kind: 'in-progress' };
  } else if (upgradeDidNotComplete) {
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

  const unavailableReason =
    action.kind === 'blocked'
      ? action.reason
      : action.kind === 'loading'
        ? t('Checking upgrade options')
        : action.kind === 'in-progress'
          ? latestUpgrade?.message
          : undefined;

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
        {action.kind === 'available' ? (
          <FlexItem>
            <Label color="orange">{t('Upgrade available')}</Label>
          </FlexItem>
        ) : action.kind === 'in-progress' ? (
          <FlexItem>
            <Label color="blue" id={unavailableReason ? undefined : reasonId}>
              {t('Upgrade in progress')}
            </Label>
          </FlexItem>
        ) : action.kind === 'blocked' ? (
          <FlexItem>
            <Label color="grey">{t('No upgrade available')}</Label>
          </FlexItem>
        ) : null}
        <FlexItem>
          <Button
            variant="link"
            isInline
            onClick={() => setOpenClusterId(cluster.id)}
            isDisabled={action.kind !== 'available'}
            aria-describedby={
              unavailableReason || action.kind === 'in-progress' ? reasonId : undefined
            }
            aria-busy={action.kind === 'loading' || undefined}
          >
            {t('Upgrade cluster')}
          </Button>
        </FlexItem>
      </Flex>
      {unavailableReason && (
        <Content component="p" id={reasonId}>
          {unavailableReason}
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
