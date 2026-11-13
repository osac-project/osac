import { useParams } from 'react-router-dom';

import { Clusters } from '@osac/types';

import { useGetResource } from '../../api/use-resource';
import { ResourceDetailsPageError } from '../Resource/ResourceDetailsPageError';
import { ResourceDetailsPageLoading } from '../Resource/ResourceDetailsPageLoading';
import ClusterDetailsPageContent from './Details/ClusterDetailsPageContent';

export const ClusterDetailsPage = () => {
  const { clusterId } = useParams() as { clusterId: string };
  const { data, isLoading, isError, error, refetch } = useGetResource(
    Clusters,
    { id: clusterId },
    { enabled: Boolean(clusterId) },
  );

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

  if (!data?.object) {
    return (
      <ResourceDetailsPageError
        parentTo="/clusters"
        parentLabel="Clusters"
        resourceLabel="cluster"
        variant="not-found"
      />
    );
  }

  return <ClusterDetailsPageContent cluster={data.object} />;
};
