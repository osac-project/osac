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
  const upgradeInProgress =
    cluster.status?.state !== ClusterState.FAILED &&
    cluster.status?.state !== ClusterState.DELETING &&
    cluster.status?.state !== ClusterState.DELETE_FAILED &&
    cluster.status?.upgradeHistory.some(
      ({ state }) =>
        state === ClusterUpgradeState.PENDING || state === ClusterUpgradeState.PROGRESSING,
    );
  const configuredVersionQuery = useGetResource(
    ClusterVersions,
    { id: configuredVersionId ?? '' },
    { enabled: Boolean(configuredVersionId) },
  );
  const catalogQuery = useListAllResources(ClusterVersions, undefined, {
    enabled: Boolean(configuredVersionId),
  });
  const configuredVersion = configuredVersionQuery.data?.object;

  let choices: ClusterUpgradeChoiceResult = { eligibleTargets: [] };
  let action: UpgradeActionState;
  if (upgradeInProgress) {
    action = { kind: 'in-progress' };
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
    choices = resolveClusterUpgradeChoices(
      cluster,
      configuredVersion,
      catalogQuery.data?.items ?? [],
      t,
    );
    action = choices.blockedReason
      ? { kind: 'blocked', reason: choices.blockedReason }
      : { kind: 'available' };
  }

  const unavailableReason =
    action.kind === 'blocked'
      ? action.reason
      : action.kind === 'loading'
        ? t('Checking upgrade options')
        : undefined;

  return (
    <>
      <Flex
        alignItems={{ default: 'alignItemsCenter' }}
        spaceItems={{ default: 'spaceItemsSm' }}
        flexWrap={{ default: 'wrap' }}
      >
        <FlexItem>{cluster.status?.observedVersion || '—'}</FlexItem>
        {action.kind === 'available' ? (
          <FlexItem>
            <Label color="orange">{t('Upgrade available')}</Label>
          </FlexItem>
        ) : action.kind === 'in-progress' ? (
          <FlexItem>
            <Label color="blue" id={reasonId}>
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
