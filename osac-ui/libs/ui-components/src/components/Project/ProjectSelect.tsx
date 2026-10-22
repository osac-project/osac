import { useMemo, useState } from 'react';
import {
  Alert,
  Divider,
  Label,
  MenuToggle,
  Select,
  SelectList,
  SelectOption,
} from '@patternfly/react-core';
import FolderOpenIcon from '@patternfly/react-icons/dist/esm/icons/folder-open-icon';

import { type Project } from '@osac/types';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';
import { getErrorMessage } from '@osac/ui-components/utils/error';

import { type ProjectTreeNode, buildProjectTree, getProjectName } from './utils';

interface FlatProject {
  project: Project;
  level: number;
}

const flattenProjectTree = (nodes: ProjectTreeNode[], level = 0): FlatProject[] =>
  nodes.flatMap(({ project, children }) => [
    { project, level },
    ...flattenProjectTree(children, level + 1),
  ]);

interface ProjectSelectProps {
  projects: Project[];
  isLoading: boolean;
  error: unknown;
  mode: 'single' | 'multiple';
  selected: string[];
  onChange: (projects: string[]) => void;
  fieldId: string;
  label?: string;
  describedBy?: string;
  isDisabled?: boolean;
}

const ProjectToggleLabel = ({
  mode,
  selectedProjects,
  isLoading,
}: Pick<ProjectSelectProps, 'mode' | 'isLoading'> & { selectedProjects: Project[] }) => {
  const { t } = useTranslation();

  if (isLoading) {
    return t('Loading projects...');
  }

  if (mode === 'multiple') {
    return (
      <>
        {t('Project')}:&nbsp;
        {selectedProjects.length
          ? selectedProjects.map((project) => getProjectName(project, t)).join(', ')
          : t('All projects')}
      </>
    );
  }

  return selectedProjects[0] ? getProjectName(selectedProjects[0], t) : t('Root');
};

const ProjectSelect = ({
  projects,
  isLoading,
  error,
  mode,
  selected,
  fieldId,
  onChange,
  isDisabled,
  describedBy,
  label,
}: ProjectSelectProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);
  const flatProjects = useMemo(() => flattenProjectTree(buildProjectTree(projects)), [projects]);
  const selectedProjects = selected
    .map((value) => projects.find((project) => project.metadata?.name === value))
    .filter((project): project is Project => project !== undefined);

  return (
    <Select
      id={`${fieldId}-select`}
      isOpen={isOpen}
      selected={selected}
      onSelect={(_, value: string) => {
        if (value === null) {
          onChange([]);
          setIsOpen(false);
          return;
        }

        if (mode === 'single') {
          onChange([value]);
          setIsOpen(false);
          return;
        }

        onChange(
          selected.includes(value)
            ? selected.filter((project) => project !== value)
            : [...selected, value],
        );
      }}
      onOpenChange={setIsOpen}
      toggle={(toggleRef) => (
        <MenuToggle
          ref={toggleRef}
          id={fieldId}
          onClick={() => setIsOpen((open) => !open)}
          isExpanded={isOpen}
          isDisabled={!!isDisabled || isLoading || (mode === 'single' && !!error)}
          isFullWidth
          icon={<FolderOpenIcon />}
          aria-describedby={describedBy}
        >
          <ProjectToggleLabel
            isLoading={isLoading}
            mode={mode}
            selectedProjects={selectedProjects}
          />
        </MenuToggle>
      )}
      shouldFocusToggleOnSelect
      aria-label={label}
    >
      <SelectList>
        {mode === 'multiple' && (
          <>
            <SelectOption value={null} isSelected={selected.length === 0}>
              {t('All projects')}
            </SelectOption>
            <Divider />
          </>
        )}
        <div className="osac-project-filter__list">
          {mode === 'multiple' && error ? (
            <Alert title={t('Failed to fetch projects')} variant="danger" isInline>
              {getErrorMessage(error)}
            </Alert>
          ) : (
            flatProjects.map(({ project, level }) => {
              const value = project.metadata?.name || '';
              return (
                <SelectOption key={project.id} value={value} isSelected={selected.includes(value)}>
                  <span>
                    {'\u00a0'.repeat(level * 4)}
                    {getProjectName(project, t)}
                    {level > 1 && (
                      <>
                        {' '}
                        <Label isCompact>{t('Nested')}</Label>
                      </>
                    )}
                  </span>
                </SelectOption>
              );
            })
          )}
        </div>
      </SelectList>
    </Select>
  );
};

export default ProjectSelect;
