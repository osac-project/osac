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

const nestedProjects = [
  makeProject('p-root', ''),
  makeProject('p-foo', 'foo'),
  makeProject('p-foo-bar', 'foo.bar'),
  makeProject('p-foo-bar-baz', 'foo.bar.baz'),
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

  it('renders nested projects as an expandable tree', async () => {
    const { user } = renderPage(nestedProjects);

    await waitFor(() => {
      expect(screen.getByRole('treegrid', { name: 'Projects' })).toBeInTheDocument();
    });

    expect(screen.getByRole('link', { name: 'Root' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'foo' })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'foo.bar' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'bar' })).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Expand project' }));

    expect(screen.getByRole('link', { name: 'bar' })).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Expand project' })).toHaveLength(1);

    await user.click(screen.getByRole('button', { name: 'Expand project' }));

    expect(screen.getByRole('link', { name: 'baz' })).toBeInTheDocument();
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

    await waitFor(() => {
      expect(screen.getByText('No projects match your search.')).toBeInTheDocument();
    });
    expect(screen.queryByText('frontend')).not.toBeInTheDocument();
  });

  it('filters projects by name', async () => {
    const { user } = renderPage();

    await waitFor(() => {
      expect(screen.getByText('frontend')).toBeInTheDocument();
    });

    const searchInput = screen.getByRole('textbox', { name: 'Search projects' });
    await user.type(searchInput, 'front');

    await waitFor(() => {
      expect(screen.queryByText('backend')).not.toBeInTheDocument();
    });
    expect(screen.getByText('frontend')).toBeInTheDocument();
  });

  it('shows "root" for projects with empty name', async () => {
    const emptyNameProject = makeProject('p-3', '');
    renderPage([emptyNameProject]);

    await waitFor(() => {
      expect(screen.getByText('Root')).toBeInTheDocument();
    });
  });
});
