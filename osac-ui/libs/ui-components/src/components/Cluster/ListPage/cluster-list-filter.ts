import { Cluster } from '@osac/types';
import { type CelFilter, cel } from '@osac/ui-components/api/cel';

import { type ClusterStatusFilter, clusterStatusFilterToState } from './clusterDisplay';

export type ClusterListFilterCriteria = {
  search?: string;
  status?: ClusterStatusFilter;
  versionNames?: readonly string[];
};

export const buildClusterListFilter = (
  criteria: ClusterListFilterCriteria,
): CelFilter<Cluster> | undefined => {
  const search = criteria.search?.trim();
  const versionNames = criteria.versionNames ?? [];
  const hasCriteria = Boolean(search || criteria.status || versionNames.length > 0);

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

    if (versionNames.length > 0) {
      clauses.push(filter.field('spec.version.name').isIn([...versionNames]));
    }

    return filter.and(...clauses);
  });
};
