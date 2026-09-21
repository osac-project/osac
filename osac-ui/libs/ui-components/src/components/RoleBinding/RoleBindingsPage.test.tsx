import { screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Role, RoleBinding, User } from '@osac/types';
import { RoleBindingState, ServiceTier } from '@osac/types';

import RoleBindingsPage from './RoleBindingsPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.mock('../../hooks/use-session', () => ({
  useSession: vi.fn(() => ({
    role: 'tenant-admin',
    username: 'testuser',
    tenantId: 'tenant-1',
    enabledServices: [ServiceTier.CAAS, ServiceTier.VMAAS, ServiceTier.BMAAS],
  })),
}));

const { useSession } = await import('../../hooks/use-session');

const makeRole = (id: string, title: string, description = ''): Role =>
  ({
    id,
    metadata: {
      name: id,
    },
    spec: { title, description },
    status: { state: 0, message: '' },
  }) as Role;

const makeRoleBinding = (
  id: string,
  roleId: string,
  userIds: string[],
  state = RoleBindingState.READY,
): RoleBinding =>
  ({
    id,
    metadata: {
      name: id,
    },
    spec: {
      role: {
        id: roleId,
        name: roleId,
      },
      users: userIds.map((uid) => ({ id: uid, name: uid })),
    },
    status: { state, message: '' },
  }) as RoleBinding;

const makeUser = (id: string, username: string, email: string): User =>
  ({
    id,
    metadata: { name: id },
    spec: { username, email },
    status: {},
  }) as User;

const defaultRoles = [
  makeRole('role-viewer', 'Viewer', 'Read-only access'),
  makeRole('role-editor', 'Editor', 'Edit resources'),
];

const defaultRoleBindings = [
  makeRoleBinding('rb-1', 'role-viewer', ['user-1', 'user-2']),
  makeRoleBinding('rb-2', 'role-editor', ['user-3'], RoleBindingState.PENDING),
];

const defaultUsers = [
  makeUser('user-1', 'alice', 'alice@example.com'),
  makeUser('user-2', 'bob', 'bob@example.com'),
  makeUser('user-3', 'charlie', 'charlie@example.com'),
];

const renderPage = ({
  roles = defaultRoles,
  roleBindings = defaultRoleBindings,
  users = defaultUsers,
}: {
  roles?: Role[];
  roleBindings?: RoleBinding[];
  users?: User[];
} = {}) =>
  renderWithProviders(<RoleBindingsPage />, {
    apiFixtures: { roles, roleBindings, users },
  });

describe('RoleBindingsPage', () => {
  beforeEach(() => {
    vi.mocked(useSession).mockReturnValue({
      role: 'tenant-admin',
      username: 'testuser',
      tenantId: 'tenant-1',
      enabledServices: [ServiceTier.CAAS, ServiceTier.VMAAS, ServiceTier.BMAAS],
      userTheme: 'system',
      resolvedTheme: 'light',
      setUserTheme: vi.fn(),
      userContrast: 'system',
      resolvedContrast: 'glass',
      setUserContrast: vi.fn(),
      projects: [],
      setProjects: vi.fn(),
    });
  });

  it('renders the page title', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Role Bindings' })).toBeInTheDocument();
    });
  });

  it('renders role binding rows with resolved role and user count', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('Viewer')).toBeInTheDocument();
      expect(screen.getByText('2 users')).toBeInTheDocument();
    });
    expect(screen.getByText('1 user')).toBeInTheDocument();
    expect(screen.getByText('Editor')).toBeInTheDocument();
  });

  it('displays status column', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('Viewer')).toBeInTheDocument();
    });

    const rows = screen.getAllByRole('row');
    const viewerRow = rows.find((row) => within(row).queryByText('Viewer'));
    expect(viewerRow).toBeDefined();
    expect(within(viewerRow as HTMLElement).getByText('Ready')).toBeInTheDocument();

    const editorRow = rows.find((row) => within(row).queryByText('Editor'));
    expect(editorRow).toBeDefined();
    expect(within(editorRow as HTMLElement).getByText('Pending')).toBeInTheDocument();
  });

  it('shows empty state when there are no role bindings', async () => {
    renderPage({ roleBindings: [] });

    await waitFor(() => {
      expect(screen.getByText('No role bindings available.')).toBeInTheDocument();
    });
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it('renders the create role binding link', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'Create role binding' })).toBeInTheDocument();
    });
  });

  it('renders actions menu for each row', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('Viewer')).toBeInTheDocument();
    });

    const actionButtons = screen.getAllByRole('button', { name: 'Actions' });
    expect(actionButtons).toHaveLength(2);
  });
});
