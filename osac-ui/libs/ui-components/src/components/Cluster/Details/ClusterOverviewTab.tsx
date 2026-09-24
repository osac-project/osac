import { Stack, StackItem } from '@patternfly/react-core';

import type { Cluster } from '@osac/types';

import { ClusterConfigurationCard } from './ClusterConfigurationCard';

interface ClusterOverviewTabProps {
  cluster: Cluster;
}

export const ClusterOverviewTab = ({ cluster }: ClusterOverviewTabProps) => {
  return (
    <Stack hasGutter>
      <StackItem>
        <ClusterConfigurationCard cluster={cluster} />
      </StackItem>
    </Stack>
  );
};
