import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { Project } from '@osac/types';
import { ProjectState } from '@osac/types';

import ProjectListPage from './ProjectListPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

const makeProject = (id: string, name: string, state = ProjectState.ACTIVE): Project =>
  ({
    id,
    metadata: {
      name,
      creationTimestamp: { seconds: BigInt(1717000000), nanos: 0 },
    },
    spec: { title: name },
    status: { state },
  }) as Project;

const defaultProjects = [
  makeProject('p-1', 'frontend', ProjectState.ACTIVE),
  makeProject('p-2', 'backend', ProjectState.PENDING),
];

const renderPage = (
  projects: Project[] = defaultProjects,
  role: 'tenant-admin' | 'tenant-user' = 'tenant-admin',
) =>
  renderWithProviders(<ProjectListPage />, {
    apiFixtures: { projects },
    session: { role, username: 'testuser', tenantId: 'tenant-1' },
  });

describe('ProjectListPage', () => {
  it('renders the page title and description', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Projects' })).toBeInTheDocument();
    });
    expect(screen.getByText('Organize resources with hierarchical projects.')).toBeInTheDocument();
  });

  it('renders project rows with names and statuses', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('frontend')).toBeInTheDocument();
    });
    expect(screen.getByText('backend')).toBeInTheDocument();
    expect(screen.getByText('Active')).toBeInTheDocument();
    expect(screen.getByText('Pending')).toBeInTheDocument();
  });

  it('shows the Create project link for tenant-admin', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'Create project' })).toHaveAttribute(
        'href',
        '/projects/create',
      );
    });
  });

  it('hides the Create project link for tenant-user', async () => {
    renderPage(defaultProjects, 'tenant-user');

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Projects' })).toBeInTheDocument();
    });
    expect(screen.queryByRole('link', { name: 'Create project' })).not.toBeInTheDocument();
  });

  it('shows empty state when there are no projects', async () => {
    renderPage([]);

    await waitFor(() => {
      expect(screen.getByText('No projects yet. Create one to get started.')).toBeInTheDocument();
    });
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it('shows filtered empty state when search matches nothing', async () => {
    const { user } = renderPage();

    await waitFor(() => {
      expect(screen.getByText('frontend')).toBeInTheDocument();
    });

    const searchInput = screen.getByRole('textbox', { name: 'Search projects' });
    await user.type(searchInput, 'nonexistent');

    expect(screen.getByText('No projects match your search.')).toBeInTheDocument();
    expect(screen.queryByText('frontend')).not.toBeInTheDocument();
  });

  it('filters projects by name', async () => {
    const { user } = renderPage();

    await waitFor(() => {
      expect(screen.getByText('frontend')).toBeInTheDocument();
    });

    const searchInput = screen.getByRole('textbox', { name: 'Search projects' });
    await user.type(searchInput, 'front');

    expect(screen.getByText('frontend')).toBeInTheDocument();
    expect(screen.queryByText('backend')).not.toBeInTheDocument();
  });

  it('shows "default" for projects with empty name', async () => {
    const emptyNameProject = makeProject('p-3', '');
    renderPage([emptyNameProject]);

    await waitFor(() => {
      expect(screen.getByText('Default')).toBeInTheDocument();
    });
  });
});
