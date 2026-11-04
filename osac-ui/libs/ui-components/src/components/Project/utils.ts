import { TFunction } from 'i18next';

import { type Project } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';

export interface ProjectTreeNode {
  project: Project;
  path: string;
  children: ProjectTreeNode[];
}

const getParentPath = (path: string) => {
  const lastSeparator = path.lastIndexOf('.');
  return lastSeparator === -1 ? '' : path.slice(0, lastSeparator);
};

/** Builds a tree from project objects whose metadata.name is a dotted full path. */
export const buildProjectTree = (projects: Project[]): ProjectTreeNode[] => {
  const nodes: ProjectTreeNode[] = projects.map((project) => ({
    project,
    path: project.metadata?.name ?? project.id,
    children: [],
  }));
  const nodesByPath = new Map(nodes.map((node) => [node.path, node]));
  const roots: ProjectTreeNode[] = [];

  nodes.forEach((node) => {
    const parent = node.path ? nodesByPath.get(getParentPath(node.path)) : undefined;
    if (parent) {
      parent.children.push(node);
    } else {
      roots.push(node);
    }
  });

  const defaultProject = nodesByPath.get('');
  if (defaultProject) {
    return [defaultProject, ...roots.filter((node) => node !== defaultProject)];
  }

  return roots;
};

/** Returns the full project paths that contain the supplied project. */
export const getProjectParentPaths = (project: Project): string[] => {
  const fullName = project.metadata?.name ?? '';
  if (!fullName) {
    return [];
  }

  const segments = fullName.split('.');
  return ['', ...segments.slice(0, -1).map((_, index) => segments.slice(0, index + 1).join('.'))];
};

export const getProjectName = (project: Project, t: TFunction): string => {
  const fullName = project.metadata?.name;
  if (fullName === '') {
    return t('Root');
  }

  const leafName = fullName?.split('.').pop() || project.id;
  const title = project.spec?.title?.trim();
  return title && title !== fullName ? title : leafName;
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
