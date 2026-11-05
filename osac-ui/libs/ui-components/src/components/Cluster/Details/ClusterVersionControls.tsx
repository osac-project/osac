import { useId, useState } from 'react';
import { Code, ConnectError } from '@connectrpc/connect';
import { Button, Content, Flex, FlexItem, Label } from '@patternfly/react-core';
import type { TFunction } from 'i18next';

import { type Cluster, ClusterState, ClusterUpgradeState, ClusterVersions } from '@osac/types';

import {
  type ClusterUpgradeChoiceResult,
  resolveClusterUpgradeChoices,
} from './clusterUpgradeChoices';
import UpgradeClusterDialog from './UpgradeClusterDialog';
import { useGetResource, useListAllResources } from '../../../api/use-resource';
import { clusterUpgradeCatalogFilter } from '../../../api/v1/cluster-versions';
import { useTranslation } from '../../../hooks/useTranslation';

interface ClusterVersionControlsProps {
  cluster: Cluster;
}

type UpgradeActionState =
  | { kind: 'loading' }
  | { kind: 'available' }
  | { kind: 'in-progress' }
  | { kind: 'blocked'; reason: string };

const blockedReasonText = (
  choices: ClusterUpgradeChoiceResult,
  desiredVersion: string | undefined,
  t: TFunction,
): string => {
  switch (choices.blockedReason) {
    case 'cluster-failed':
      return t('The cluster is in a failed state.');
    case 'cluster-deleting':
      return t('The cluster is being deleted.');
    case 'cluster-delete-failed':
      return t('The cluster deletion failed.');
    case 'configured-version-unresolved':
      return t('The configured cluster version could not be found.');
    case 'configured-version-invalid':
      return t('The configured cluster version is invalid.');
    case 'control-plane-observation-unavailable':
      return t('The current control plane version is not available yet.');
    case 'node-set-observation-unavailable':
      return choices.blockedObservedVersion
        ? t('Node set {{nodeSetId}} reported an invalid observed version: {{observedVersion}}.', {
            nodeSetId: choices.blockedNodeSetId,
            observedVersion: choices.blockedObservedVersion,
          })
        : t('Node set {{nodeSetId}} has not reported an observed version.', {
            nodeSetId: choices.blockedNodeSetId,
          });
    case 'components-not-converged':
      return choices.blockedNodeSetId
        ? t('Node set {{nodeSetId}} is at {{observedVersion}}; wait for {{desiredVersion}}.', {
            nodeSetId: choices.blockedNodeSetId,
            observedVersion: choices.blockedObservedVersion,
            desiredVersion,
          })
        : t('The control plane is at {{observedVersion}}; wait for {{desiredVersion}}.', {
            observedVersion: choices.blockedObservedVersion,
            desiredVersion,
          });
    case 'unsupported-skew':
      return t('Available targets exceed the supported node set version skew.');
    case 'no-eligible-target':
      return t('No eligible upgrade target is available.');
    default:
      return t('Upgrade eligibility is unavailable.');
  }
};

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
  const [dialogOpen, setDialogOpen] = useState(false);
  const tenant = cluster.metadata?.tenant;
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
    { enabled: Boolean(configuredVersionId && tenant && !upgradeInProgress), retry: false },
  );
  const catalogQuery = useListAllResources(
    ClusterVersions,
    tenant ? clusterUpgradeCatalogFilter(tenant) : undefined,
    {
      enabled: Boolean(configuredVersionId && tenant && !upgradeInProgress),
      refetchInterval: false,
    },
  );
  const fetchedVersion = configuredVersionQuery.data?.object;
  const configuredVersion =
    fetchedVersion?.metadata?.tenant === tenant || fetchedVersion?.metadata?.tenant === 'shared'
      ? fetchedVersion
      : undefined;
  const notFound =
    configuredVersionQuery.error instanceof ConnectError &&
    configuredVersionQuery.error.code === Code.NotFound;

  let choices: ClusterUpgradeChoiceResult = { eligibleTargets: [] };
  let action: UpgradeActionState;
  if (upgradeInProgress) {
    action = { kind: 'in-progress' };
  } else if (!tenant) {
    action = { kind: 'blocked', reason: t('Upgrade eligibility is unavailable.') };
  } else if (
    configuredVersionQuery.isLoading ||
    catalogQuery.isLoading ||
    catalogQuery.isPlaceholderData
  ) {
    action = { kind: 'loading' };
  } else if ((configuredVersionQuery.isError && !notFound) || catalogQuery.isError) {
    action = { kind: 'blocked', reason: t('Could not load upgrade options.') };
  } else {
    choices = resolveClusterUpgradeChoices(cluster, configuredVersion, catalogQuery.data ?? []);
    action = choices.blockedReason
      ? {
          kind: 'blocked',
          reason: blockedReasonText(choices, configuredVersion?.spec?.version, t),
        }
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
            onClick={() => setDialogOpen(true)}
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
          onClose={() => setDialogOpen(false)}
        />
      )}
    </>
  );
};

export default ClusterVersionControls;
