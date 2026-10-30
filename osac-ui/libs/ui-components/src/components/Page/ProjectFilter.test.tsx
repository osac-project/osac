import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { type Project } from '@osac/types';

import ProjectFilter from './ProjectFilter';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.mock('@osac/ui-components/hooks/use-session', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@osac/ui-components/hooks/use-session')>()),
  useSession: vi.fn(),
}));

const { useSession } = await import('@osac/ui-components/hooks/use-session');

const makeProject = (id: string, name: string, parent = ''): Project =>
  ({
    id,
    metadata: { name, project: parent },
    spec: { title: name },
  }) as Project;

const projects = [
  makeProject('project-root', ''),
  makeProject('project-foo', 'foo'),
  makeProject('project-foo-bar', 'foo.bar', 'foo'),
];

describe('ProjectFilter', () => {
  const setProjects = vi.fn();

  const mockSessionProjects = (selectedProjects: string[]) => {
    vi.mocked(useSession).mockReturnValue({
      projects: selectedProjects,
      setProjects,
      role: 'tenant-admin',
      tenantId: 'tenant-1',
    } as unknown as ReturnType<typeof useSession>);
  };

  beforeEach(() => {
    vi.clearAllMocks();
    mockSessionProjects([]);
  });

  it('renders projects as an indented list and stores the full path when selected', async () => {
    const { user } = renderWithProviders(<ProjectFilter />, { apiFixtures: { projects } });

    await user.click(await screen.findByRole('button', { name: /All projects/ }));

    expect(screen.queryByRole('tree')).not.toBeInTheDocument();
    expect(
      screen.getByRole('listbox').querySelector('.osac-project-filter__list'),
    ).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'Root' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'foo' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: /bar Nested/ })).toBeInTheDocument();

    await user.click(screen.getByRole('option', { name: /bar Nested/ }));

    expect(setProjects).toHaveBeenCalledWith(['foo.bar']);
  });

  it('keeps the menu open while adding and removing selected projects', async () => {
    mockSessionProjects(['foo']);
    const { user, rerender } = renderWithProviders(<ProjectFilter />, {
      apiFixtures: { projects },
    });

    await user.click(await screen.findByRole('button', { name: /Project:\s*foo/ }));
    await user.click(screen.getByRole('option', { name: /bar Nested/ }));

    expect(setProjects).toHaveBeenLastCalledWith(['foo', 'foo.bar']);
    expect(screen.getByRole('listbox')).toBeInTheDocument();

    mockSessionProjects(['foo', 'foo.bar']);
    rerender(<ProjectFilter />);
    await user.click(screen.getByRole('option', { name: 'foo' }));

    expect(setProjects).toHaveBeenLastCalledWith(['foo.bar']);
    expect(screen.getByRole('listbox')).toBeInTheDocument();
  });

  it('distinguishes selecting Root from clearing the filter with All projects', async () => {
    const { user } = renderWithProviders(<ProjectFilter />, { apiFixtures: { projects } });

    await user.click(await screen.findByRole('button', { name: /All projects/ }));
    await user.click(screen.getByRole('option', { name: 'Root' }));

    expect(setProjects).toHaveBeenLastCalledWith(['']);
    expect(screen.getByRole('listbox')).toBeInTheDocument();

    await user.click(screen.getByRole('option', { name: 'All projects' }));

    expect(setProjects).toHaveBeenLastCalledWith([]);
    await waitFor(() => {
      expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
    });
  });
});
