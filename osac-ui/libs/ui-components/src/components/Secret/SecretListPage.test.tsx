import { create } from '@bufbuild/protobuf';
import { createRouterTransport } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ProjectSchema, Projects, SecretSchema, SecretType, Secrets } from '@osac/types';
import { wrapWithAuthInterceptor } from '@osac/ui-components/test-utils/createMockConnectTransport';
import { renderWithProviders } from '@osac/ui-components/test-utils/TestProviders';

import SecretListPage from './SecretListPage';

vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) =>
  window.setTimeout(() => callback(0), 0),
);

const myProject = create(ProjectSchema, {
  id: 'project-my-project',
  metadata: { name: 'my-project' },
});

const pullSecret = create(SecretSchema, {
  id: 'secret-1',
  metadata: { name: 'pull-creds', project: 'default' },
  type: SecretType.PULL_SECRET,
});

const createTransport = (items = [pullSecret]) => {
  let listFilter: string | undefined;
  let totalsFilter: string | undefined;

  const transport = wrapWithAuthInterceptor(
    createRouterTransport((router) => {
      router.service(Secrets, {
        list: (req) => {
          const limit = Number(req.limit);
          if (limit === 0) {
            totalsFilter = req.filter;
            return { items: [], total: items.length };
          }
          listFilter = req.filter;
          if (req.filter) {
            return { items: [], total: 0 };
          }
          return { items, total: items.length };
        },
        get: ({ id }) => ({ object: items.find((item) => item.id === id) }),
      });
      router.service(Projects, {
        list: () => ({ items: [myProject], total: 1 }),
        get: () => ({ object: myProject }),
      });
    }),
  );

  return {
    transport,
    getListFilter: () => listFilter,
    getTotalsFilter: () => totalsFilter,
  };
};

describe('SecretListPage', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('renders secrets from the list API', async () => {
    const { transport } = createTransport();
    renderWithProviders(<SecretListPage />, { transport, session: { role: 'tenant-admin' } });

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'pull-creds' })).toBeInTheDocument();
    });
    expect(screen.getByText('1 secret')).toBeInTheDocument();
  });

  it('shows the type filter toggle', async () => {
    const { transport } = createTransport();
    renderWithProviders(<SecretListPage />, { transport, session: { role: 'tenant-admin' } });

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'All types' })).toBeInTheDocument();
    });
  });

  it('shows an empty state when no secrets are returned', async () => {
    const { transport } = createTransport([]);
    renderWithProviders(<SecretListPage />, { transport, session: { role: 'tenant-admin' } });

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: 'No secrets found', level: 2 }),
      ).toBeInTheDocument();
    });
    expect(screen.getByText('No secrets are available yet.')).toBeInTheDocument();
  });

  it('shows a filter-specific empty state when the search matches nothing', async () => {
    const { transport } = createTransport();
    renderWithProviders(<SecretListPage />, {
      transport,
      session: { role: 'tenant-admin' },
      routerEntries: ['/?search=no-such-secret'],
    });

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: 'No secrets match your filters', level: 2 }),
      ).toBeInTheDocument();
    });
    expect(screen.getByText('Try a different project, type, or search term.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Clear all filters' })).toBeInTheDocument();
  });

  it('clears search and project filters together', async () => {
    const { transport, getListFilter } = createTransport();
    const { user } = renderWithProviders(<SecretListPage />, {
      transport,
      session: { role: 'tenant-admin' },
      routerEntries: ['/?project=my-project&search=no-such-secret'],
    });

    await waitFor(() => {
      expect(screen.getByRole('textbox', { name: 'Search secrets' })).toHaveValue('no-such-secret');
      expect(screen.getByRole('button', { name: /Project:\s*my-project/ })).toBeInTheDocument();
    });

    const clearFiltersButtons = await screen.findAllByRole('button', { name: 'Clear all filters' });
    await user.click(clearFiltersButtons[0]);

    await waitFor(() => {
      expect(screen.getByRole('textbox', { name: 'Search secrets' })).toHaveValue('');
    });
    await waitFor(() => {
      expect(getListFilter()).toBeUndefined();
    });

    expect(screen.getByRole('button', { name: /Project:\s*All projects/ })).toBeInTheDocument();
  });

  it('limits administrators to shared and system tenants and hides the project filter', async () => {
    const { transport, getListFilter, getTotalsFilter } = createTransport();
    renderWithProviders(<SecretListPage />, { transport, session: { role: 'admin' } });

    await waitFor(() => {
      expect(getListFilter()).toBe('this.metadata.tenant in ["shared", "system"]');
      expect(getTotalsFilter()).toBe('this.metadata.tenant in ["shared", "system"]');
    });

    expect(screen.queryByRole('button', { name: /Project:/ })).not.toBeInTheDocument();
  });
});
