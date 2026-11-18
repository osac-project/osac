import type { Cluster } from '@osac/types';

import { ClusterConfigurationCard } from './ClusterConfigurationCard';

interface ClusterOverviewTabProps {
  cluster: Cluster;
}

export const ClusterOverviewTab = ({ cluster }: ClusterOverviewTabProps) => (
  <ClusterConfigurationCard cluster={cluster} />
);
