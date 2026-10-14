import { useSession } from './use-session';
import { type CelFilter, cel } from '../api/cel';

export interface ProjectScopedResource {
  metadata?: {
    project: string;
  };
}

export const useProjectFilterQuery = <T extends ProjectScopedResource>():
  CelFilter<T> | undefined => {
  const { projects } = useSession();
  return projects.length
    ? (cel<ProjectScopedResource>((filter) =>
        filter.field('metadata.project').isIn(projects),
      ) as CelFilter<T>)
    : undefined;
};
