import { useMemo } from 'react';
import { Table, Tbody, Th, Thead, Tr } from '@patternfly/react-table';

import { type Cluster, type ClusterVersion } from '@osac/types';

import { ClusterTableRow } from './ClusterTableRow';
import { useTranslation } from '../../../hooks/useTranslation';

interface ClustersTableProps {
  clusters: Cluster[];
  resolveClusterVersion: (cluster: Cluster) => ClusterVersion | undefined;
  /** Adds a Tenant column, for cloud provider admins who see every tenant's workloads. */
  showTenant?: boolean;
}

export const ClustersTable = ({
  clusters,
  resolveClusterVersion,
  showTenant = false,
}: ClustersTableProps) => {
  const { t } = useTranslation();

  const columnLabels = useMemo(
    () => ({
      name: t('Name'),
      tenant: t('Tenant'),
      status: t('Status'),
      project: t('Project'),
      configuration: t('Configuration'),
      created: t('Created'),
      actions: t('Actions'),
    }),
    [t],
  );

  return (
    <Table aria-label={t('Clusters')} variant="compact" borders>
      <Thead>
        <Tr>
          <Th>{columnLabels.name}</Th>
          {showTenant ? <Th>{columnLabels.tenant}</Th> : null}
          <Th>{columnLabels.status}</Th>
          <Th>{columnLabels.project}</Th>
          <Th>{columnLabels.configuration}</Th>
          <Th>{columnLabels.created}</Th>
          <Th aria-label={columnLabels.actions} />
        </Tr>
      </Thead>
      <Tbody>
        {clusters.map((cluster) => (
          <ClusterTableRow
            key={cluster.id}
            cluster={cluster}
            clusterVersion={resolveClusterVersion(cluster)}
            columnLabels={columnLabels}
            showTenant={showTenant}
          />
        ))}
      </Tbody>
    </Table>
  );
};
