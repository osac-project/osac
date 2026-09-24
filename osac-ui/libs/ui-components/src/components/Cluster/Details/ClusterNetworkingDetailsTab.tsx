import type { Cluster } from '@osac/types';

import ClusterNetworkingCard from './ClusterNetworkingCard';

interface ClusterNetworkingDetailsTabProps {
  cluster: Cluster;
}

export const ClusterNetworkingDetailsTab = ({ cluster }: ClusterNetworkingDetailsTabProps) => {
  return <ClusterNetworkingCard cluster={cluster} />;
};
