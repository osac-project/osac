import { describe, expect, it } from 'vitest';

import { ClusterVersionState } from '@osac/types';

import {
  CLUSTER_VERSION_ACTIVE_LIST_FILTER,
  CLUSTER_VERSION_ALL_STATES_LIST_FILTER,
  clusterVersionNamesFilter,
} from './cluster-versions';

describe('cluster version list filters', () => {
  it('restricts the active filter to ACTIVE/DEPRECATED enabled versions', () => {
    expect(CLUSTER_VERSION_ACTIVE_LIST_FILTER).toBe(
      `(this.spec.state == ${ClusterVersionState.ACTIVE} || this.spec.state == ${ClusterVersionState.DEPRECATED}) && this.spec.enabled == true`,
    );
  });

  it('references spec.state and spec.enabled so the all-states filter returns every state', () => {
    expect(CLUSTER_VERSION_ALL_STATES_LIST_FILTER).toContain('this.spec.state');
    expect(CLUSTER_VERSION_ALL_STATES_LIST_FILTER).toContain('this.spec.enabled');
    expect(CLUSTER_VERSION_ALL_STATES_LIST_FILTER).toContain(`${ClusterVersionState.OBSOLETE}`);
  });

  it('compares the enum spec.state with == and never list membership', () => {
    expect(CLUSTER_VERSION_ALL_STATES_LIST_FILTER).toContain(
      `this.spec.state == ${ClusterVersionState.OBSOLETE}`,
    );
    expect(CLUSTER_VERSION_ALL_STATES_LIST_FILTER).not.toContain('this.spec.state in');
  });

  it('scopes the name filter to the given versions while retaining all states', () => {
    const filter = clusterVersionNamesFilter(['v4.17', 'v4.15']);

    expect(filter).toContain('this.metadata.name in ["v4.17", "v4.15"]');
    expect(filter).toContain('this.spec.state');
    expect(filter).toContain('this.spec.enabled');
    expect(filter).toContain(`this.spec.state == ${ClusterVersionState.OBSOLETE}`);
    expect(filter).not.toContain('this.spec.state in');
  });
});
