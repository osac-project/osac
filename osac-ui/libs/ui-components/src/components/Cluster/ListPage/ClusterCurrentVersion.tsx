import { Flex, FlexItem } from '@patternfly/react-core';

import type { Cluster, ClusterVersion } from '@osac/types';

import ClusterVersionLifecycleLabel from '../ClusterVersionLifecycleLabel';

interface ClusterCurrentVersionProps {
  cluster: Cluster;
  clusterVersion?: ClusterVersion;
}

const ClusterCurrentVersion = ({ cluster, clusterVersion }: ClusterCurrentVersionProps) => {
  const observedVersion = cluster.status?.observedVersion?.trim();
  const matchingVersion =
    observedVersion && clusterVersion?.spec?.version === observedVersion
      ? clusterVersion
      : undefined;

  return (
    <Flex alignItems={{ default: 'alignItemsCenter' }} gap={{ default: 'gapSm' }}>
      <FlexItem>{observedVersion || '—'}</FlexItem>
      {matchingVersion && (
        <FlexItem>
          <ClusterVersionLifecycleLabel clusterVersion={matchingVersion} />
        </FlexItem>
      )}
    </Flex>
  );
};

export default ClusterCurrentVersion;
