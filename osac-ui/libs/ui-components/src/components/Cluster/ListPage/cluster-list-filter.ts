import { Cluster } from '@osac/types';
import { type CelFilter, cel } from '@osac/ui-components/api/cel';

import { type ClusterStatusFilter, clusterStatusFilterToState } from './clusterDisplay';

export type ClusterListFilterCriteria = {
  search?: string;
  status?: ClusterStatusFilter;
  observedVersions?: readonly string[];
};

export const buildClusterListFilter = (
  criteria: ClusterListFilterCriteria,
): CelFilter<Cluster> | undefined => {
  const search = criteria.search?.trim();
  const observedVersions = criteria.observedVersions;
  const hasCriteria = Boolean(search || criteria.status || observedVersions !== undefined);

  if (!hasCriteria) {
    return undefined;
  }

  return cel<Cluster>((filter) => {
    const clauses: CelFilter<Cluster>[] = [];

    if (search) {
      clauses.push(
        filter.or(
          filter.field('metadata.name').contains(search),
          filter.field('id').contains(search),
        ),
      );
    }

    if (criteria.status) {
      clauses.push(
        filter.field('status.state').equals(clusterStatusFilterToState(criteria.status)),
      );
    }

    if (observedVersions !== undefined) {
      clauses.push(filter.field('status.observedVersion').isIn(observedVersions));
    }

    return filter.and(...clauses);
  });
};
