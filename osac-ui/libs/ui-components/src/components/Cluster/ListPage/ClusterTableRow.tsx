import { Td, Tr } from '@patternfly/react-table';

import { type Cluster, ClusterState, type ClusterVersion } from '@osac/types';
import ClusterResources from '@osac/ui-components/components/Cluster/ListPage/ClusterResources';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import ClusterActionsMenu from '../ClusterActionsMenu';
import { formatClusterCreatedAt } from './clusterDisplay';
import { ClusterStatusLabel } from './ClusterStatusLabel';

export type ClusterTableColumn =
  | 'name'
  | 'status'
  | 'project'
  | 'configuration'
  | 'created'
  | 'actions';

interface ClusterTableRowProps {
  cluster: Cluster;
  clusterVersion?: ClusterVersion;
  columnLabels: Record<ClusterTableColumn, string>;
}

export const ClusterTableRow = ({
  cluster,
  clusterVersion,
  columnLabels,
}: ClusterTableRowProps) => {
  const { t } = useTranslation();
  const locked = cluster.status?.state === ClusterState.DELETING;

  return (
    <Tr key={cluster.id}>
      <Td dataLabel={columnLabels.name}>
        <ResourceNameField
          resource={cluster}
          detailsUrl={locked ? undefined : `/clusters/${encodeURIComponent(cluster.id)}`}
        />
      </Td>
      <Td dataLabel={columnLabels.status}>
        <ClusterStatusLabel state={cluster.status?.state} />
      </Td>
      <Td dataLabel={columnLabels.project}>{cluster.metadata?.project || t('Default')}</Td>
      <Td dataLabel={columnLabels.configuration}>
        <ClusterResources cluster={cluster} clusterVersion={clusterVersion} />
      </Td>
      <Td dataLabel={columnLabels.created}>{formatClusterCreatedAt(cluster.metadata)}</Td>
      <Td dataLabel={columnLabels.actions} isActionCell>
        {locked ? null : <ClusterActionsMenu cluster={cluster} />}
      </Td>
    </Tr>
  );
};
