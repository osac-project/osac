import { create } from '@bufbuild/protobuf';
import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  type Cluster,
  ClusterSchema,
  ClusterState,
  type ClusterVersion,
  ClusterVersionSchema,
  ClusterVersionState,
} from '@osac/types';

import { ClustersTable } from './ClustersTable';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const makeCluster = (id: string, versionName: string, observedVersion = ''): Cluster =>
  create(ClusterSchema, {
    id,
    metadata: { name: id, project: 'Root' },
    status: {
      state: ClusterState.READY,
      observedVersion,
      nodeSets: { worker: { size: 3 } },
    },
    spec: {
      version: { name: versionName },
      nodeSets: { worker: { size: 3 } },
      network: { podCidr: '10.128.0.0/24', serviceCidr: '10.1.0.0/24' },
    },
  });

const makeClusterVersion = (version: string, state: ClusterVersionState): ClusterVersion =>
  create(ClusterVersionSchema, {
    id: version,
    metadata: { name: version },
    spec: { version, state, enabled: true },
  });

const clusterVersions = [
  makeClusterVersion('4.17.0', ClusterVersionState.ACTIVE),
  makeClusterVersion('4.16.0', ClusterVersionState.DEPRECATED),
  makeClusterVersion('4.15.0', ClusterVersionState.OBSOLETE),
];

const renderTable = (clusters: Cluster[], versions: ClusterVersion[] = clusterVersions) =>
  renderWithProviders(
    <ClustersTable
      clusters={clusters}
      resolveClusterVersion={(cluster) =>
        versions.find((version) => version.spec?.version === cluster.status?.observedVersion)
      }
    />,
  );

const clusterRow = (name: string) => {
  const row = screen.getByText(name).closest('tr');
  if (!row) {
    throw new Error('Expected a cluster table row');
  }
  return within(row);
};

describe('ClustersTable observed versions', () => {
  it('shows observed versions and their lifecycle badges in a Current version column', () => {
    renderTable([
      makeCluster('c-active', 'desired-4.18', '4.17.0'),
      makeCluster('c-obsolete', 'desired-4.18', '4.15.0'),
    ]);

    expect(screen.getByRole('columnheader', { name: 'Current version' })).toBeInTheDocument();
    expect(clusterRow('c-active').getByText('4.17.0')).toBeInTheDocument();
    expect(clusterRow('c-active').getByText('Active')).toBeInTheDocument();
    expect(clusterRow('c-obsolete').getByText('4.15.0')).toBeInTheDocument();
    expect(clusterRow('c-obsolete').getByText('Obsolete')).toBeInTheDocument();
    expect(screen.queryByRole('columnheader', { name: 'Desired version' })).not.toBeInTheDocument();
    expect(screen.queryByText('Cluster version')).not.toBeInTheDocument();
    expect(screen.getAllByText('Node sets')).toHaveLength(2);
    expect(screen.getAllByText('1 pool · 3 of 3 nodes ready')).toHaveLength(2);
  });

  it('does not infer a current version or lifecycle from the desired version', () => {
    renderTable([makeCluster('c-missing', '4.17.0')]);

    const versionCell = screen
      .getByText('c-missing')
      .closest('tr')
      ?.querySelector('td[data-label="Current version"]');
    expect(versionCell).toHaveTextContent('—');
    expect(clusterRow('c-missing').queryByText('Active')).not.toBeInTheDocument();
    expect(clusterRow('c-missing').queryByText('4.17.0')).not.toBeInTheDocument();
  });

  it('hides the lifecycle badge when the observed version has no catalog match', () => {
    renderTable([makeCluster('c-unknown', '4.17.0', '4.19.0')]);

    expect(clusterRow('c-unknown').getByText('4.19.0')).toBeInTheDocument();
    expect(clusterRow('c-unknown').queryByText('Active')).not.toBeInTheDocument();
  });
});
