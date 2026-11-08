import { Route, Routes, useParams } from 'react-router-dom';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { Project } from '@osac/types';
import { ProjectState } from '@osac/types';

import ProjectCreatePage from './ProjectCreatePage';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const ProjectDetailsStub = () => {
  const { id } = useParams() as { id: string };
  return <h1>{id}</h1>;
};

const makeProject = (id: string, name: string, project = ''): Project =>
  ({
    id,
    metadata: { name, project },
    spec: { title: name },
    status: { state: ProjectState.ACTIVE },
  }) as Project;

const defaultProjects = [makeProject('p-1', 'default')];

const renderPage = (projects: Project[] = defaultProjects) =>
  renderWithProviders(
    <Routes>
      <Route path="/projects/create" element={<ProjectCreatePage />} />
      <Route path="/projects/:id" element={<ProjectDetailsStub />} />
      <Route path="/projects" element={<div>Project list</div>} />
    </Routes>,
    {
      routerEntries: ['/projects/create'],
      apiFixtures: { projects },
    },
  );

describe('ProjectCreatePage', () => {
  it('renders the page heading and breadcrumb', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Create project' })).toBeInTheDocument();
    });
    expect(screen.getByRole('button', { name: 'Projects' })).toBeInTheDocument();
  });

  it('renders form fields', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('Parent project')).toBeInTheDocument();
    });
    expect(screen.getByLabelText(/^Name/)).toBeInTheDocument();
    expect(screen.getByLabelText('Title')).toBeInTheDocument();
  });

  it('renders Create and Cancel buttons', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Create' })).toBeInTheDocument();
    });
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
  });

  it('navigates back to project list on Cancel', async () => {
    const { user } = renderPage();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    await waitFor(() => {
      expect(screen.getByText('Project list')).toBeInTheDocument();
    });
  });

  it('navigates to created project details after successful create', async () => {
    const { user } = renderPage();

    await waitFor(() => {
      expect(screen.getByLabelText(/^Name/)).toBeInTheDocument();
    });

    await user.type(screen.getByLabelText(/^Name/), 'my-project');
    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'new-project-1' })).toBeInTheDocument();
    });
  });
});
