import type { TFunction } from 'i18next';
import { describe, expect, it } from 'vitest';

import type { Project } from '@osac/types';

import {
  DEFAULT_PROJECT_FILTER_VALUE,
  getProjectFilterPath,
  getProjectName,
  getSelectableProjects,
  normalizeProjectFilterPath,
  resolveProjectFromFilterPath,
} from './utils';

const makeProject = (id: string, name: string, project = ''): Project =>
  ({
    id,
    metadata: { name, project },
    spec: { title: name || 'Default title' },
  }) as Project;

describe('project filter utils', () => {
  it('uses a stable filter path for the default project', () => {
    const defaultProject = makeProject('default-project', '');

    expect(getProjectFilterPath(defaultProject)).toBe(DEFAULT_PROJECT_FILTER_VALUE);
    expect(normalizeProjectFilterPath('')).toBe(DEFAULT_PROJECT_FILTER_VALUE);
  });

  it('displays the default project as Default', () => {
    const defaultProject = makeProject('default-project', '');
    const t = ((key: string) => key) as TFunction;

    expect(getProjectName(defaultProject, t)).toBe('Default');
  });

  it('includes only one default project in selectable options', () => {
    const projects = [
      makeProject('default-1', ''),
      makeProject('default-2', ''),
      makeProject('team-a', 'team-a'),
    ];

    expect(getSelectableProjects(projects)).toEqual([projects[0], projects[2]]);
  });

  it('resolves the default project from legacy empty filter paths', () => {
    const projects = [makeProject('default-project', ''), makeProject('team-a', 'team-a')];

    expect(resolveProjectFromFilterPath(projects, '')).toEqual(projects[0]);
    expect(resolveProjectFromFilterPath(projects, DEFAULT_PROJECT_FILTER_VALUE)).toEqual(
      projects[0],
    );
  });
});
