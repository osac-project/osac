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

  it('builds a filter for the observed cluster version', () => {
    const filter = buildClusterListFilter({ observedVersions: ['4.17.0', '4.15.0'] });
    expect(filter).toBe('this.status.observed_version in ["4.17.0", "4.15.0"]');
  });

  it('matches no clusters when selected catalog versions cannot be resolved', () => {
    expect(buildClusterListFilter({ observedVersions: [] })).toBe(
      'this.status.observed_version in []',
    );
  });

  it('combines search, status, and version filters', () => {
    const filter = buildClusterListFilter({
      search: 'dev',
      status: 'progressing',
      observedVersions: ['4.17.0'],
    });
    expect(filter).toContain('this.metadata.name.contains("dev")');
    expect(filter).toContain(`this.status.state == ${ClusterState.PROGRESSING}`);
    expect(filter).toContain('this.status.observed_version in ["4.17.0"]');
    expect(filter).toContain(' && ');
  });
});
