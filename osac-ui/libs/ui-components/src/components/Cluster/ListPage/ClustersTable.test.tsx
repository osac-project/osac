import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
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

const makeCluster = (id: string, versionName: string): Cluster =>
  create(ClusterSchema, {
    id,
    metadata: { name: id, project: 'Root' },
    status: {
      state: ClusterState.READY,
      nodeSets: {
        worker: { size: 3 },
      },
    },
    spec: {
      version: { name: versionName },
      nodeSets: {
        worker: { size: 3 },
      },
      network: { podCidr: '10.128.0.0/24', serviceCidr: '10.1.0.0/24' },
    },
  });

const makeClusterVersion = (
  name: string,
  version: string,
  state: ClusterVersionState,
  enabled = true,
): ClusterVersion =>
  create(ClusterVersionSchema, {
    id: name,
    metadata: { name },
    spec: { version, state, enabled },
  });

const clusterVersions = [
  makeClusterVersion('v4.17', '4.17.0', ClusterVersionState.ACTIVE),
  makeClusterVersion('v4.16', '4.16.0', ClusterVersionState.DEPRECATED),
  makeClusterVersion('v4.15', '4.15.0', ClusterVersionState.OBSOLETE),
  makeClusterVersion('v4.14', '4.14.0', ClusterVersionState.ACTIVE, false),
];

const makeResolveClusterVersion = (versions: ClusterVersion[]) => {
  const byName = new Map(versions.map((version) => [version.metadata?.name, version]));
  return (cluster: Cluster): ClusterVersion | undefined => {
    const versionRef = cluster.spec?.version;
    if (!versionRef?.name) {
      return undefined;
    }
    return byName.get(versionRef.name);
  };
};

const renderTable = (clusters: Cluster[], versions: ClusterVersion[] = clusterVersions) =>
  renderWithProviders(
    <ClustersTable
      clusters={clusters}
      resolveClusterVersion={makeResolveClusterVersion(versions)}
    />,
  );

describe('ClustersTable version join', () => {
  it('renders list columns and resolved OpenShift versions in configuration', () => {
    renderTable([makeCluster('c-active', 'v4.17'), makeCluster('c-obsolete', 'v4.15')]);

    expect(screen.getByText('OpenShift 4.17.0')).toBeInTheDocument();
    expect(screen.getByText('OpenShift 4.15.0')).toBeInTheDocument();
    expect(screen.getAllByText('Ready')).toHaveLength(2);
    expect(screen.getAllByText('Root')).toHaveLength(2);
    expect(screen.getByText('Configuration')).toBeInTheDocument();
    expect(screen.getAllByText('Node sets')).toHaveLength(2);
    expect(screen.getAllByText('1 pool · 3 of 3 nodes ready')).toHaveLength(2);
  });

  it('falls back to unresolved version labels in configuration', () => {
    renderTable([makeCluster('c-missing', 'v-deleted')], []);

    expect(screen.getByText('OpenShift v-deleted')).toBeInTheDocument();
  });

  it('renders clusters without a version reference', () => {
    renderTable([create(ClusterSchema, { id: 'c-none', metadata: { name: 'c-none' } })]);

    expect(screen.getByText('c-none')).toBeInTheDocument();
    expect(screen.getAllByText('—').length).toBeGreaterThan(0);
  });
});
