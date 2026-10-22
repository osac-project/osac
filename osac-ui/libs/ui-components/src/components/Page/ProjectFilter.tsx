import { useEffect } from 'react';
import { useSearchParams } from 'react-router-dom';

import { type Project, Projects } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import { useListAllResources } from '@osac/ui-components/api/use-resource';
import { serializePageFilter } from '@osac/ui-components/hooks/use-page-filter';
import { PROJECT_FILTER_PARAM, useSession } from '@osac/ui-components/hooks/use-session';

import ProjectSelect from '../Project/ProjectSelect';

import './ProjectFilter.css';

const ProjectFilter = () => {
  const { projects, setProjects } = useSession();
  const { data, isLoading, error } = useListAllResources(
    Projects,
    cel<Project>((builder) => builder.field('metadata.tenant').notEquals('shared')),
  );
  const [searchParams, setSearchParams] = useSearchParams();

  useEffect(() => {
    if (projects !== undefined) {
      const serialized = serializePageFilter(projects);
      if (!serialized) {
        setSearchParams(
          (prev) => {
            const next = new URLSearchParams(prev);
            next.delete(PROJECT_FILTER_PARAM);
            return next;
          },
          { replace: true },
        );
        return;
      }

      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          next.set(PROJECT_FILTER_PARAM, serialized);
          return next;
        },
        { replace: true },
      );
    }
  }, [searchParams, setSearchParams, projects]);

  const selectedProjects: Project[] = [];

  projects.forEach((p) => {
    const project = data?.find((d) => d.metadata?.name === p);
    if (project) {
      selectedProjects.push(project);
    }
  });

  const hasAllProjects = projects.length === selectedProjects.length;

  useEffect(() => {
    if (!isLoading && !hasAllProjects) {
      setProjects(selectedProjects.map((p) => p.metadata?.name || ''));
    }
    // trigger based on hasAllProjects + isLoading only
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasAllProjects, isLoading]);

  return (
    <ProjectSelect
      mode="multiple"
      projects={data ?? []}
      selected={projects}
      onChange={setProjects}
      isLoading={isLoading}
      error={error}
      fieldId="metadata.project"
    />
  );
};

export default ProjectFilter;
