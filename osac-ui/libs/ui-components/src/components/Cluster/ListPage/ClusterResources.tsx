import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
} from '@patternfly/react-core';

import { type Cluster, type ClusterVersion } from '@osac/types';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import ClusterCurrentVersion from './ClusterCurrentVersion';

interface ClusterResourcesProps {
  cluster: Cluster;
  clusterVersion?: ClusterVersion;
  includeVersion?: boolean;
}

const ClusterResources = ({
  cluster,
  clusterVersion,
  includeVersion = true,
}: ClusterResourcesProps) => {
  const { t } = useTranslation();

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
      {includeVersion && (
        <DescriptionListGroup>
          <DescriptionListTerm>{t('Current version')}</DescriptionListTerm>
          <DescriptionListDescription>
            <ClusterCurrentVersion cluster={cluster} clusterVersion={clusterVersion} />
          </DescriptionListDescription>
        </DescriptionListGroup>
      )}
      <DescriptionListGroup>
        <DescriptionListTerm>{t('Node sets')}</DescriptionListTerm>
        <DescriptionListDescription>{nodeSetsSummary}</DescriptionListDescription>
      </DescriptionListGroup>
    </DescriptionList>
  );
};

export default ClusterResources;
