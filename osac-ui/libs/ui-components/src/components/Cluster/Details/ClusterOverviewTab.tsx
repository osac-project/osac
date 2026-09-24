import { Stack, StackItem } from '@patternfly/react-core';

import type { Cluster } from '@osac/types';

import ClusterAutoProvisionedResources from './ClusterAutoProvisionedResources';
import { ClusterConfigurationCard } from './ClusterConfigurationCard';
import ClusterNetworkingCard from './ClusterNetworkingCard';

interface ClusterOverviewTabProps {
  cluster: Cluster;
}

export const ClusterOverviewTab = ({ cluster }: ClusterOverviewTabProps) => {
  const showAutoProvisioned = cluster.spec?.autoExternalIpAttachment === true;

  return (
    <Stack hasGutter>
      <StackItem>
        <ClusterConfigurationCard cluster={cluster} />
      </StackItem>
      <StackItem>
        <ClusterNetworkingCard cluster={cluster} />
      </StackItem>
      {showAutoProvisioned && (
        <StackItem>
          <ClusterAutoProvisionedResources clusterId={cluster.id} />
        </StackItem>
      )}
    </Stack>
  );
};
