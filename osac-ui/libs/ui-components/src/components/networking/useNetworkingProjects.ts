import { useCallback } from 'react';

import { type Project, Projects } from '@osac/types';

import { cel } from '../../api/cel';
import { useListAllResources } from '../../api/use-resource';

/** The same query as the project filter, so both share one cache entry. */
const PROJECTS_FILTER = cel<Project>((filter) =>
  filter.field('metadata.tenant').notEquals('shared'),
);

/** Finds a resource's project by its full path (`metadata.project`) and tenant. */
export const useNetworkingProjects = () => {
  const { data } = useListAllResources(Projects, PROJECTS_FILTER);
  const findProject = useCallback(
    (path = '', tenant?: string): Project | undefined =>
      (data?.items ?? []).find(
        (project) =>
          (project.metadata?.name ?? '') === path &&
          (!tenant || project.metadata?.tenant === tenant),
      ),
    [data],
  );
  return { findProject };
};
