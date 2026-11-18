import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import type { Role, RoleBinding, User } from '@osac/types';
import { RoleBindingState } from '@osac/types';
import { getViewTypePrefKey } from '@osac/ui-components/hooks/use-view-type';

import RoleBindingsPage from './RoleBindingsPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

const VIEW_PREF_KEY = getViewTypePrefKey('role-bindings-list');

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
    session: { role: 'tenant-admin', username: 'testuser', tenantId: 'tenant-1' },
  });

describe('RoleBindingsPage', () => {
  beforeEach(() => {
    localStorage.setItem(VIEW_PREF_KEY, 'list');
  });

  afterEach(() => {
    localStorage.removeItem(VIEW_PREF_KEY);
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
      expect(screen.getByText('No role bindings found')).toBeInTheDocument();
    });
    expect(screen.getByText('No role bindings are available yet.')).toBeInTheDocument();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it('renders the create role binding link', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'Create role binding' })).toBeInTheDocument();
    });
  });

  it('renders role filter in the toolbar', async () => {
    renderPage();

    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: 'Filter role bindings by role' }),
      ).toHaveTextContent('All roles');
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
