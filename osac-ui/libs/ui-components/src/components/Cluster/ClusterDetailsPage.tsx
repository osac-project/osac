import { useParams } from 'react-router-dom';
import type { TFunction } from 'i18next';

import { ClusterVersions } from '@osac/types';

import { useListAllResources } from '../../api/use-resource';
import { useCluster } from '../../api/v1/cluster';
import {
  clusterUpgradeCatalogFilter,
  useConfiguredClusterVersion,
} from '../../api/v1/cluster-versions';
import { useTranslation } from '../../hooks/useTranslation';
import { ResourceDetailsPageError } from '../Resource/ResourceDetailsPageError';
import { ResourceDetailsPageLoading } from '../Resource/ResourceDetailsPageLoading';
import ClusterDetailsPageContent from './Details/ClusterDetailsPageContent';
import { resolveClusterUpgradeChoices } from './Details/clusterUpgradeChoices';
import type { ClusterUpgradeBlockedReason } from './Details/clusterUpgradeChoices';
import type { UpgradeActionState } from './Details/ClusterVersionRail';

const blockedReasonText = (reason: ClusterUpgradeBlockedReason, t: TFunction): string => {
  switch (reason) {
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
      return t('Current node set versions are not available yet.');
    case 'components-not-converged':
      return t('Wait for the control plane and node sets to reach the desired version.');
    case 'no-eligible-target':
      return t('No eligible upgrade target is available.');
    case 'node-set-ahead':
      return t('A node set is newer than the available upgrade targets.');
    case 'unsupported-skew':
      return t('Available targets exceed the supported node set version skew.');
  }
};

export const ClusterDetailsPage = () => {
  const { clusterId } = useParams() as { clusterId: string };
  const { t } = useTranslation();
  const { data: cluster, isLoading, isError, error, refetch } = useCluster(clusterId);
  const configuredVersion = useConfiguredClusterVersion(
    cluster?.spec?.version,
    cluster?.metadata?.tenant,
  );
  const catalog = useListAllResources(
    ClusterVersions,
    clusterUpgradeCatalogFilter(cluster?.metadata?.tenant),
    {
      enabled: Boolean(cluster),
      refetchInterval: false,
    },
  );

  if (isLoading) {
    return (
      <ResourceDetailsPageLoading
        parentTo="/clusters"
        parentLabel="Clusters"
        tabLabels={['Overview', 'Networking', 'Node sets']}
        tabsId="cluster-detail-tabs"
      />
    );
  }

  if (isError) {
    return (
      <ResourceDetailsPageError
        parentTo="/clusters"
        parentLabel="Clusters"
        resourceLabel="cluster"
        error={error}
        onRetry={() => void refetch()}
      />
    );
  }

  if (!cluster) {
    return (
      <ResourceDetailsPageError
        parentTo="/clusters"
        parentLabel="Clusters"
        resourceLabel="cluster"
        variant="not-found"
      />
    );
  }

  let action: UpgradeActionState;
  let eligibleVersions = (catalog.data ?? []).filter(
    (version) =>
      version.metadata?.tenant === cluster.metadata?.tenant ||
      version.metadata?.tenant === 'shared',
  );
  if (configuredVersion.isLoading || catalog.isLoading) {
    action = { kind: 'loading' };
    eligibleVersions = [];
  } else if (configuredVersion.isError || catalog.isError) {
    action = { kind: 'blocked', reason: t('Could not load upgrade options.') };
    eligibleVersions = [];
  } else {
    const choices = resolveClusterUpgradeChoices(cluster, configuredVersion.data, eligibleVersions);
    eligibleVersions = choices.blockedReason ? [] : choices.eligibleTargets;
    action = choices.blockedReason
      ? { kind: 'blocked', reason: blockedReasonText(choices.blockedReason, t) }
      : { kind: 'available' };
  }

  return <ClusterDetailsPageContent cluster={cluster} upgrade={{ action, eligibleVersions }} />;
};
