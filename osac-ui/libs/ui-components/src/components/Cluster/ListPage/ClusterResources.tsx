import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
} from '@patternfly/react-core';

import { type Cluster, ClusterUpgradeState, type ClusterVersion } from '@osac/types';
import { formatClusterCatalogVersionLabel } from '@osac/ui-components/components/catalog/clusterCatalogItemResourceDisplay';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { GuestOsIcon } from '../../shared/GuestOsIcon';
import ClusterVersionLifecycleLabel from '../ClusterVersionLifecycleLabel';

interface ClusterResourcesProps {
  cluster: Cluster;
  clusterVersion?: ClusterVersion;
}

const ClusterResources = ({ cluster, clusterVersion }: ClusterResourcesProps) => {
  const { t } = useTranslation();
  const observedVersion = cluster.status?.observedVersion?.trim();
  const versionLabel = formatClusterCatalogVersionLabel(observedVersion) || '—';
  const matchingVersion =
    observedVersion && clusterVersion?.spec?.version === observedVersion
      ? clusterVersion
      : undefined;
  const latestControlPlaneAttempt = cluster.status?.upgradeHistory.find(
    (entry) => entry.component?.target.case === 'controlPlane',
  );
  const activeTarget =
    latestControlPlaneAttempt?.state === ClusterUpgradeState.PENDING ||
    latestControlPlaneAttempt?.state === ClusterUpgradeState.PROGRESSING
      ? latestControlPlaneAttempt.targetVersion.trim()
      : undefined;

  const specNodeSets = cluster.spec?.nodeSets;
  const statusNodeSets = cluster.status?.nodeSets ?? {};

  const keys = specNodeSets ? Object.keys(specNodeSets) : [];

  let desired = 0;
  let ready = 0;
  if (specNodeSets) {
    for (const key of keys) {
      desired += specNodeSets?.[key].size ?? 0;
      ready += statusNodeSets[key]?.size ?? 0;
    }
  }

  const poolsCountText = t('{{count}} pool', { count: keys.length });
  const nodeSetsSummary =
    keys.length > 0 && desired > 0
      ? `${poolsCountText} · ${t('{{ready}} of {{desired}} nodes ready', {
          ready,
          desired,
        })}`
      : '-';

  return (
    <DescriptionList isHorizontal isCompact>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('Cluster version')}</DescriptionListTerm>
        <DescriptionListDescription>
          <Flex flexWrap={{ default: 'nowrap' }} gap={{ default: 'gapXs' }}>
            {observedVersion ? (
              <FlexItem>
                <GuestOsIcon os="rhel" size="sm" />
              </FlexItem>
            ) : null}
            <FlexItem>
              {versionLabel}
              {observedVersion && activeTarget ? ` → ${activeTarget}` : null}
            </FlexItem>
            {matchingVersion && (
              <FlexItem>
                <ClusterVersionLifecycleLabel clusterVersion={matchingVersion} />
              </FlexItem>
            )}
          </Flex>
        </DescriptionListDescription>
      </DescriptionListGroup>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('Node sets')}</DescriptionListTerm>
        <DescriptionListDescription>{nodeSetsSummary}</DescriptionListDescription>
      </DescriptionListGroup>
    </DescriptionList>
  );
};

export default ClusterResources;
