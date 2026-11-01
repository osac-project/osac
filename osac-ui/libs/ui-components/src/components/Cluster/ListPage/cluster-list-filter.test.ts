import { describe, expect, it } from 'vitest';

import { ClusterState } from '@osac/types';

import { buildClusterListFilter } from './cluster-list-filter';

describe('buildClusterListFilter', () => {
  it('returns undefined when no criteria are set', () => {
    expect(buildClusterListFilter({})).toBeUndefined();
  });

  it('builds a search filter', () => {
    const filter = buildClusterListFilter({ search: 'prod' });
    expect(filter).toContain('this.metadata.name.contains("prod")');
    expect(filter).toContain('this.id.contains("prod")');
  });

  it('builds a status filter', () => {
    const filter = buildClusterListFilter({ status: 'ready' });
    expect(filter).toBe(`this.status.state == ${ClusterState.READY}`);
  });

  it('builds a cluster version filter', () => {
    const filter = buildClusterListFilter({ versionNames: ['v4.17', 'v4.15'] });
    expect(filter).toBe('this.spec.version.name in ["v4.17", "v4.15"]');
  });

  it('combines search, status, and version filters', () => {
    const filter = buildClusterListFilter({
      search: 'dev',
      status: 'progressing',
      versionNames: ['v4.17'],
    });
    expect(filter).toContain('this.metadata.name.contains("dev")');
    expect(filter).toContain(`this.status.state == ${ClusterState.PROGRESSING}`);
    expect(filter).toContain('this.spec.version.name in ["v4.17"]');
    expect(filter).toContain(' && ');
  });
});
