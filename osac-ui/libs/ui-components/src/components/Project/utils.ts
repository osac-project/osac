import { TFunction } from 'i18next';

import { Project } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';

/** Stored in URL/session for the tenant default project (metadata.name === ''). */
export const DEFAULT_PROJECT_FILTER_VALUE = '__default__';

export const isDefaultProject = (project: Project): boolean => project.metadata?.name === '';

export const getProjectName = (project: Project, t: TFunction): string => {
  if (project.metadata?.name === '') {
    return t('Default');
  }
  return project.spec?.title || project.metadata?.name || project.id;
};

export const getFullProjectPath = (project: Project | undefined) => {
  return project?.metadata?.project
    ? `${project.metadata.project}.${project.metadata?.name}`
    : project?.metadata?.name || '';
};

export const getProjectFilterPath = (project: Project | undefined): string => {
  if (!project) {
    return '';
  }

  if (isDefaultProject(project)) {
    return DEFAULT_PROJECT_FILTER_VALUE;
  }

  return getFullProjectPath(project);
};

export const normalizeProjectFilterPath = (path: string): string =>
  path === '' ? DEFAULT_PROJECT_FILTER_VALUE : path;

export const resolveProjectFromFilterPath = (
  projects: Project[],
  path: string,
): Project | undefined =>
  projects.find((project) => getProjectFilterPath(project) === normalizeProjectFilterPath(path));

export const getSelectableProjects = (projects: Project[]): Project[] => {
  let includesDefaultProject = false;

  return projects.filter((project) => {
    if (!isDefaultProject(project)) {
      return true;
    }

    if (includesDefaultProject) {
      return false;
    }

    includesDefaultProject = true;
    return true;
  });
};

export const fullProjectPathToQueryFilter = (fullProjectPath: string) => {
  if (!fullProjectPath.includes('.')) {
    return cel<Project>((filter) =>
      filter.and(
        filter.field('metadata.tenant').notEquals('shared'),
        filter.field('metadata.name').equals(fullProjectPath),
      ),
    );
  }

  const lastIndex = fullProjectPath.lastIndexOf('.');

  const parent = fullProjectPath.slice(0, lastIndex);
  const name = fullProjectPath.slice(lastIndex + 1);

  return cel<Project>((filter) =>
    filter.and(
      filter.field('metadata.tenant').notEquals('shared'),
      filter.field('metadata.name').equals(name),
      filter.field('metadata.project').equals(parent),
    ),
  );
};
