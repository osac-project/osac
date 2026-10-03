import { useCallback, useEffect, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  Alert,
  Divider,
  Flex,
  FlexItem,
  MenuToggle,
  Select,
  SelectList,
  SelectOption,
  Skeleton,
} from '@patternfly/react-core';
import { RhUiFolderOpenFillIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-folder-open-fill-icon';

import { Project } from '@osac/types';
import { useAllProjects } from '@osac/ui-components/api/v1/project';
import { serializePageFilter } from '@osac/ui-components/hooks/use-page-filter';
import {
  PROJECT_FILTER_PARAM,
  getProjectFilterStorageKey,
  useSession,
} from '@osac/ui-components/hooks/use-session';
import { useUserPreferences } from '@osac/ui-components/hooks/use-user-preferences';
import { getErrorMessage } from '@osac/ui-components/utils/error';

import { useTranslation } from '../../hooks/useTranslation';
import {
  DEFAULT_PROJECT_FILTER_VALUE,
  getFullProjectPath,
  getProjectFilterPath,
  getProjectName,
  getSelectableProjects,
  isDefaultProject,
  resolveProjectFromFilterPath,
} from '../Project/utils';

const ALL_OPTION_VALUE = '__all__';

const toSessionProjects = (project: Project | undefined): string[] => {
  if (!project) {
    return [];
  }

  return [isDefaultProject(project) ? '' : getFullProjectPath(project)];
};

const serializeProjectFilterForUrl = (projects: string[]): string | null => {
  if (projects.length === 0) {
    return null;
  }

  if (projects.length === 1 && projects[0] === '') {
    return DEFAULT_PROJECT_FILTER_VALUE;
  }

  return serializePageFilter(projects);
};

const ProjectFilter = () => {
  const { t } = useTranslation();
  const { projects, setProjects, username } = useSession();
  const [, setStoredProjects] = useUserPreferences(getProjectFilterStorageKey(username));
  const [isOpen, setIsOpen] = useState(false);
  const { data, isLoading, error } = useAllProjects();
  const [searchParams, setSearchParams] = useSearchParams();

  const setProjectFilter = useCallback(
    (next: string[]) => {
      setProjects(next);

      if (next.length === 1 && next[0] === '') {
        setStoredProjects(DEFAULT_PROJECT_FILTER_VALUE);
      }
    },
    [setProjects, setStoredProjects],
  );

  useEffect(() => {
    const serialized = serializeProjectFilterForUrl(projects);
    const current = searchParams.get(PROJECT_FILTER_PARAM);

    if ((!serialized && !current) || serialized === current) {
      return;
    }

    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (!serialized) {
          next.delete(PROJECT_FILTER_PARAM);
        } else {
          next.set(PROJECT_FILTER_PARAM, serialized);
        }
        return next;
      },
      { replace: true },
    );
  }, [projects, searchParams, setSearchParams]);

  const selectableProjects = data ? getSelectableProjects(data) : [];

  const selectedProject =
    projects.length > 0 ? resolveProjectFromFilterPath(selectableProjects, projects[0]) : undefined;
  const selectedPath = selectedProject ? getProjectFilterPath(selectedProject) : undefined;
  const selection = selectedPath ?? ALL_OPTION_VALUE;
  const selectedLabel = selectedProject ? getProjectName(selectedProject, t) : t('All projects');
  const expectedProjects = toSessionProjects(selectedProject);
  const isNormalized =
    projects.length === expectedProjects.length &&
    (expectedProjects.length === 0 || projects[0] === expectedProjects[0]) &&
    projects[0] !== DEFAULT_PROJECT_FILTER_VALUE;

  useEffect(() => {
    if (!isLoading && !error && data && !isNormalized) {
      setProjectFilter(expectedProjects);
    }
    // don't trigger on expectedProjects change
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, error, isLoading, isNormalized]);

  let items = (
    <>
      {selectableProjects.map((p) => {
        const path = getProjectFilterPath(p);
        return (
          <SelectOption key={p.id} value={path} isSelected={selectedPath === path}>
            {getProjectName(p, t)}
          </SelectOption>
        );
      })}
    </>
  );

  if (isLoading) {
    items = (
      <>
        <SelectOption isDisabled>
          <Skeleton />
        </SelectOption>
        <SelectOption isDisabled>
          <Skeleton />
        </SelectOption>
        <SelectOption isDisabled>
          <Skeleton />
        </SelectOption>
      </>
    );
  }

  if (error) {
    items = (
      <Alert title={t('Failed to fetch projects')} variant="danger" isInline>
        {getErrorMessage(error)}
      </Alert>
    );
  }

  return (
    <Select
      isOpen={isOpen}
      selected={selection}
      onSelect={(_event, value) => {
        if (value === ALL_OPTION_VALUE) {
          setProjectFilter([]);
        } else if (value === DEFAULT_PROJECT_FILTER_VALUE) {
          setProjectFilter(['']);
        } else {
          setProjectFilter([value as string]);
        }
        setIsOpen(false);
      }}
      onOpenChange={setIsOpen}
      shouldFocusToggleOnSelect
      toggle={(toggleRef) => (
        <MenuToggle
          ref={toggleRef}
          onClick={() => setIsOpen((open) => !open)}
          isExpanded={isOpen}
          aria-label={t('Filter by project')}
        >
          <Flex gap={{ default: 'gapXs' }} flexWrap={{ default: 'nowrap' }}>
            <FlexItem>
              <RhUiFolderOpenFillIcon />
            </FlexItem>
            <FlexItem>{t('Project: {{selectedLabel}}', { selectedLabel })}</FlexItem>
          </Flex>
        </MenuToggle>
      )}
    >
      <SelectList>
        <SelectOption value={ALL_OPTION_VALUE} isSelected={selectedPath === undefined}>
          {t('All projects')}
        </SelectOption>
        <Divider />
        {items}
      </SelectList>
    </Select>
  );
};

export default ProjectFilter;
