import type { ReactNode } from 'react';
import { type MessageInitShape, create } from '@bufbuild/protobuf';
import { render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  type Cluster,
  ClusterConditionType,
  ClusterSchema,
  ClusterState,
  ClusterStatusSchema,
  type ClusterVersion,
  ClusterVersionSchema,
  ClusterVersionState,
} from '@osac/types';

import { ClustersTable } from './ClustersTable';
import { createMockConnectTransport } from '../../test-utils/createMockConnectTransport';
import { TestProviders } from '../../test-utils/TestProviders';

const makeCluster = (
  id: string,
  versionName: string,
  status?: MessageInitShape<typeof ClusterStatusSchema>,
): Cluster =>
  create(ClusterSchema, {
    id,
    metadata: { name: id },
    spec: { version: { name: versionName } },
    status,
  });

const progressing = (reason?: string) => ({
  state: ClusterState.PROGRESSING,
  conditions: [{ type: ClusterConditionType.PROGRESSING, reason }],
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

const renderTable = (clusters: Cluster[], versions: ClusterVersion[] = clusterVersions) => {
  let callCount = 0;
  let capturedFilter: string | undefined;
  const transport = createMockConnectTransport(
    {},
    {
      onClusterVersionList: (req) => {
        callCount += 1;
        capturedFilter = req.filter;
        return { items: versions };
      },
    },
  );
  const wrapper = ({ children }: { children: ReactNode }) => (
    <TestProviders transport={transport}>{children}</TestProviders>
  );
  const result = render(<ClustersTable clusters={clusters} />, { wrapper });
  return {
    ...result,
    getCallCount: () => callCount,
    getCapturedFilter: () => capturedFilter,
  };
};

describe('ClustersTable Status and activity', () => {
  it('shows one status label above the current activity', () => {
    renderTable([makeCluster('c-progress', '', progressing('WorkersJoining'))]);

    expect(screen.getByRole('columnheader', { name: 'Status' })).toBeInTheDocument();
    expect(screen.queryByRole('columnheader', { name: 'State' })).not.toBeInTheDocument();
    const statusCell = screen.getByText('WorkersJoining').closest('[data-label="Status"]');
    expect(statusCell?.querySelector('.pf-v6-c-label')).toHaveTextContent('Provisioning');
    expect(statusCell?.querySelector('small')).toHaveTextContent('WorkersJoining');
    expect(statusCell?.querySelectorAll('.pf-v6-c-label')).toHaveLength(1);
  });

  it('marks a stalled provisioning stage as a warning', () => {
    renderTable([makeCluster('c-stalled', '', progressing('Stalled'))]);

    const warning = screen.getByLabelText('Warning: Provisioning stalled');
    expect(warning).toHaveTextContent('Provisioning');
    expect(warning).toHaveClass('pf-m-orange');
    const statusCell = warning.closest('[data-label="Status"]');
    expect(statusCell?.querySelector('small')).toHaveTextContent('Stalled');
    expect(statusCell?.querySelectorAll('.pf-v6-c-label')).toHaveLength(1);
  });

  it('uses Stage unknown for the backend StageUnknown reason', () => {
    renderTable([makeCluster('c-unknown', '', progressing('StageUnknown'))]);

    expect(screen.getByText('Stage unknown')).toBeInTheDocument();
    expect(screen.queryByText('StageUnknown')).not.toBeInTheDocument();
  });

  it('uses Stage unknown when no progressing condition is returned', () => {
    renderTable([
      makeCluster('c-no-condition', '', {
        state: ClusterState.PROGRESSING,
        conditions: [{ type: ClusterConditionType.READY, reason: 'ReadyCondition' }],
      }),
    ]);

    expect(screen.getByText('Stage unknown')).toBeInTheDocument();
    expect(screen.queryByText('ReadyCondition')).not.toBeInTheDocument();
  });

  it.each(['', '   '])('uses Stage unknown for a blank reason %j', (reason) => {
    renderTable([makeCluster('c-blank', '', progressing(reason))]);

    expect(screen.getByText('Stage unknown')).toBeInTheDocument();
  });

  it.each([
    [ClusterState.READY, 'Ready'],
    [ClusterState.FAILED, 'Failed'],
    [ClusterState.DELETING, 'Deleting'],
    [ClusterState.DELETE_FAILED, 'Delete failed'],
    [ClusterState.UNSPECIFIED, 'Unknown'],
  ])('shows %s without an old provisioning stage', (state, stateText) => {
    renderTable([
      makeCluster('c-other-state', '', {
        state,
        conditions: [{ type: ClusterConditionType.PROGRESSING, reason: 'WorkersJoining' }],
      }),
    ]);

    expect(screen.getByText(stateText)).toBeInTheDocument();
    expect(screen.queryByText('WorkersJoining')).not.toBeInTheDocument();
    expect(screen.queryByText('Stage unknown')).not.toBeInTheDocument();
  });

  it('shows Unknown without activity when public status is absent', () => {
    renderTable([makeCluster('c-no-status', '')]);

    expect(screen.getByText('Unknown')).toBeInTheDocument();
    expect(screen.queryByText('Stage unknown')).not.toBeInTheDocument();
  });

  it('replaces the prior reason when the public cluster prop changes', () => {
    const { rerender } = renderTable([
      makeCluster('c-updating', '', progressing('AllocatingWorkers')),
    ]);
    expect(screen.getByText('AllocatingWorkers')).toBeInTheDocument();

    rerender(
      <ClustersTable clusters={[makeCluster('c-updating', '', progressing('WorkersJoining'))]} />,
    );
    expect(screen.queryByText('AllocatingWorkers')).not.toBeInTheDocument();
    expect(screen.getByText('WorkersJoining')).toBeInTheDocument();

    rerender(
      <ClustersTable clusters={[makeCluster('c-updating', '', { state: ClusterState.READY })]} />,
    );
    expect(screen.queryByText('WorkersJoining')).not.toBeInTheDocument();
    expect(screen.getByText('Ready')).toBeInTheDocument();
  });
});

describe('ClustersTable version join', () => {
  it('keeps resolved version strings without a Lifecycle column', async () => {
    renderTable([
      makeCluster('c-active', 'v4.17'),
      makeCluster('c-deprecated', 'v4.16'),
      makeCluster('c-obsolete', 'v4.15'),
      makeCluster('c-disabled', 'v4.14'),
    ]);

    expect(await screen.findByText('4.17.0')).toBeInTheDocument();
    expect(screen.getByText('4.16.0')).toBeInTheDocument();
    expect(screen.getByText('4.15.0')).toBeInTheDocument();
    expect(screen.getByText('4.14.0')).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Version' })).toBeInTheDocument();
    expect(screen.queryByRole('columnheader', { name: 'Lifecycle' })).not.toBeInTheDocument();
    expect(screen.queryByText('Deprecated')).not.toBeInTheDocument();
    expect(screen.queryByText('Obsolete')).not.toBeInTheDocument();
  });

  it('issues a single scoped List referencing the version names plus all states', async () => {
    const { getCallCount, getCapturedFilter } = renderTable([
      makeCluster('c-active', 'v4.17'),
      makeCluster('c-obsolete', 'v4.15'),
    ]);

    expect(await screen.findByText('4.17.0')).toBeInTheDocument();
    expect(getCallCount()).toBe(1);
    const filter = getCapturedFilter() ?? '';
    expect(filter).toContain('this.metadata.name in ["v4.17", "v4.15"]');
    expect(filter).toContain('this.spec.state');
    expect(filter).toContain('this.spec.enabled');
    // Enum spec.state must never use list membership (see OSAC-4206).
    expect(filter).not.toContain('this.spec.state in');
  });

  it('does not issue a versions List when no cluster references a version', async () => {
    const { getCallCount } = renderTable([
      create(ClusterSchema, { id: 'c-none', metadata: { name: 'c-none' } }),
    ]);

    expect(await screen.findByText('c-none')).toBeInTheDocument();
    await waitFor(() => expect(getCallCount()).toBe(0));
  });

  it('falls back to the raw reference name when a version is unresolved', async () => {
    renderTable([makeCluster('c-missing', 'v-deleted')], []);

    expect(await screen.findByText('v-deleted')).toBeInTheDocument();
    expect(screen.queryByRole('columnheader', { name: 'Lifecycle' })).not.toBeInTheDocument();
  });

  it('shows a skeleton while cluster versions are loading', () => {
    const { container } = renderTable([makeCluster('c-active', 'v4.17')]);

    expect(container.querySelectorAll('.pf-v6-c-skeleton').length).toBeGreaterThan(0);
    expect(screen.queryByText('4.17.0')).toBeNull();
  });
});
