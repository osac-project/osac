import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
  Label,
} from '@patternfly/react-core';

import { type Cluster, ClusterUpgradeState, type ClusterVersion } from '@osac/types';
import { formatClusterCatalogVersionLabel } from '@osac/ui-components/components/catalog/clusterCatalogItemResourceDisplay';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { GuestOsIcon } from '../../shared/GuestOsIcon';
import ClusterVersionLifecycleLabel from '../ClusterVersionLifecycleLabel';
import {
  getActiveControlPlaneUpgradeTarget,
  getControlPlaneUpgradeHistory,
} from './clusterDisplay';

interface ClusterResourcesProps {
  cluster: Cluster;
  clusterVersion?: ClusterVersion;
}

const ClusterResources = ({ cluster, clusterVersion }: ClusterResourcesProps) => {
  const { t } = useTranslation();
  const observedVersion = cluster.status?.observedVersion;
  const versionLabel = formatClusterCatalogVersionLabel(observedVersion) || '—';
  const observedCatalogVersion =
    observedVersion && clusterVersion?.spec?.version === observedVersion
      ? clusterVersion
      : undefined;
  const activeTarget = getActiveControlPlaneUpgradeTarget(cluster);
  const upgradeState = getControlPlaneUpgradeHistory(cluster)[0]?.state;
  const upgradePending = upgradeState === ClusterUpgradeState.PENDING;
  const upgradeProgressing = upgradeState === ClusterUpgradeState.PROGRESSING;
  const upgradeFailed = upgradeState === ClusterUpgradeState.FAILED;
  const upgradeTimedOut = upgradeState === ClusterUpgradeState.TIMED_OUT;
  let upgradeLabel;
  if (upgradePending) {
    upgradeLabel = <Label status="info">{t('Upgrade pending')}</Label>;
  } else if (upgradeProgressing) {
    upgradeLabel = <Label status="info">{t('Upgrade in progress')}</Label>;
  } else if (upgradeFailed) {
    upgradeLabel = <Label status="danger">{t('Upgrade failed')}</Label>;
  } else if (upgradeTimedOut) {
    upgradeLabel = <Label status="warning">{t('Upgrade timed out')}</Label>;
  }

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
            {upgradeLabel ? (
              <FlexItem>{upgradeLabel}</FlexItem>
            ) : observedCatalogVersion ? (
              <FlexItem>
                <ClusterVersionLifecycleLabel clusterVersion={observedCatalogVersion} />
              </FlexItem>
            ) : null}
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
