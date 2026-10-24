import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
} from '@patternfly/react-core';

import { type Cluster, type ClusterVersion } from '@osac/types';
import { formatClusterCatalogVersionLabel } from '@osac/ui-components/components/catalog/clusterCatalogItemResourceDisplay';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { GuestOsIcon } from '../../shared/GuestOsIcon';

interface ClusterResourcesProps {
  cluster: Cluster;
  clusterVersion?: ClusterVersion;
}

const ClusterResources = ({ cluster, clusterVersion }: ClusterResourcesProps) => {
  const { t } = useTranslation();

  const versionLabel = formatClusterCatalogVersionLabel(clusterVersion, cluster.spec?.version);

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
            {versionLabel !== '-' ? (
              <FlexItem>
                <GuestOsIcon os="rhel" size="sm" />
              </FlexItem>
            ) : null}
            <FlexItem>{versionLabel || '—'}</FlexItem>
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
