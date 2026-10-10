import { useParams } from 'react-router-dom';

import { useCluster } from '../../api/v1/cluster';
import { ResourceDetailsPageError } from '../Resource/ResourceDetailsPageError';
import { ResourceDetailsPageLoading } from '../Resource/ResourceDetailsPageLoading';
import ClusterDetailsPageContent from './Details/ClusterDetailsPageContent';

export const ClusterDetailsPage = () => {
  const { clusterId } = useParams() as { clusterId: string };
  const { data: cluster, isLoading, isError, error, refetch } = useCluster(clusterId);

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

  return <ClusterDetailsPageContent cluster={cluster} />;
};
