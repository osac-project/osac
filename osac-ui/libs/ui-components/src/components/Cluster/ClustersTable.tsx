import { useMemo } from 'react';
import { Skeleton } from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import { type Cluster, ClusterState } from '@osac/types';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';

import ClusterActionsMenu from './ClusterActionsMenu';
import { ClusterStatusLabel } from './ClusterStatusLabel';
import { clusterVersionNamesFilter, useClusterVersions } from '../../api/v1/cluster-versions';
import { useTranslation } from '../../hooks/useTranslation';
import ExternalLink from '../Primitives/ExternalLink';
import { Timestamp } from '../Primitives/Timestamp';

interface ClustersTableProps {
  clusters: Cluster[];
}

export const ClustersTable = ({ clusters }: ClustersTableProps) => {
  const { t } = useTranslation();

  // Only fetch the versions these clusters reference (by spec.version.name),
  // in one List call — scales as the catalog grows and paginates.
  const versionNames = useMemo(
    () => [
      ...new Set(
        clusters
          .map((cluster) => cluster.spec?.version?.name)
          .filter((name): name is string => !!name),
      ),
    ],
    [clusters],
  );
  const { data: clusterVersions = [], isLoading: isClusterVersionsLoading } = useClusterVersions(
    { filter: clusterVersionNamesFilter(versionNames) },
    { enabled: versionNames.length > 0 },
  );

  // Clusters reference versions by spec.version.name, so key the catalog by name.
  const clusterVersionByName = useMemo(
    () => new Map(clusterVersions.map((cv) => [cv.metadata?.name, cv])),
    [clusterVersions],
  );

  return (
    <Table aria-label={t('Clusters')} variant="compact">
      <Thead>
        <Tr>
          <Th>{t('Name')}</Th>
          <Th>{t('Status')}</Th>
          <Th>{t('Version')}</Th>
          <Th>{t('API URL')}</Th>
          <Th>{t('Created')}</Th>
          <Th aria-label={t('Actions')} />
        </Tr>
      </Thead>
      <Tbody>
        {clusters.map((cluster) => {
          const locked = cluster.status?.state === ClusterState.DELETING;
          const apiUrl = cluster.status?.apiUrl;
          const versionRef = cluster.spec?.version;
          const clusterVersion = versionRef?.name
            ? clusterVersionByName.get(versionRef.name)
            : undefined;

          return (
            <Tr key={cluster.id}>
              <Td dataLabel={t('Name')}>
                <ResourceNameField
                  resource={cluster}
                  detailsUrl={locked ? undefined : `/clusters/${encodeURIComponent(cluster.id)}`}
                />
              </Td>
              <Td dataLabel={t('Status')}>
                <ClusterStatusLabel status={cluster.status} activityInLabel />
              </Td>
              <Td dataLabel={t('Version')}>
                {isClusterVersionsLoading ? (
                  <Skeleton width="80px" />
                ) : (
                  clusterVersion?.spec?.version || versionRef?.name || '—'
                )}
              </Td>
              <Td dataLabel={t('API URL')}>
                {locked ? '—' : <ExternalLink href={apiUrl} showUnsafeAsText />}
              </Td>
              <Td dataLabel={t('Created')}>
                <Timestamp value={cluster.metadata?.creationTimestamp} />
              </Td>
              <Td dataLabel={t('Actions')} isActionCell>
                {locked ? null : <ClusterActionsMenu cluster={cluster} />}
              </Td>
            </Tr>
          );
        })}
      </Tbody>
    </Table>
  );
};
