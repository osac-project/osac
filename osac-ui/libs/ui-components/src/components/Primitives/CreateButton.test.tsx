import type { ReactElement } from 'react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { BareMetalInstances, SelfSubjectAccessReviews } from '@osac/types';
import { StorageBackends } from '@osac/types/private';

import CreateButton from './CreateButton';
import { ApiProvider } from '../../api/api-context';
import { useCreateResource } from '../../api/use-resource';

const mockSession = vi.hoisted(() => ({ role: 'tenant-user' as 'admin' | 'tenant-user' }));

vi.mock('../../hooks/use-session', () => ({
  useSession: () => mockSession,
}));

const renderWithTransport = (
  ui: ReactElement,
  transport: ReturnType<typeof createRouterTransport>,
) => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });

  return {
    ...render(
      <ApiProvider transport={transport}>
        <QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>
      </ApiProvider>,
    ),
    queryClient,
    user: userEvent.setup(),
  };
};

const renderWithPermission = (ui: ReactElement, allowed = true) => {
  const transport = createRouterTransport((router) => {
    router.service(SelfSubjectAccessReviews, {
      create: () => ({ object: { status: { allowed } } }),
    });
  });

  return renderWithTransport(ui, transport);
};

const CreateOperationConsumer = () => {
  const { mutate: create, error } = useCreateResource(BareMetalInstances);

  return (
    <>
      <CreateButton service={BareMetalInstances} onClick={() => create({})}>
        Create item
      </CreateButton>
      {error && <div role="alert">{error.message}</div>}
    </>
  );
};

afterEach(() => {
  mockSession.role = 'tenant-user';
  vi.clearAllMocks();
});

describe('CreateButton', () => {
  describe('rendering', () => {
    it('renders as a primary button with a plus icon', () => {
      renderWithPermission(<CreateButton service={BareMetalInstances}>Create item</CreateButton>);

      const button = screen.getByRole('button', { name: 'Create item' });
      expect(button).toHaveClass('pf-m-primary');
      expect(button.querySelector('.pf-v6-c-button__icon')).not.toBeNull();
    });

    it('applies a custom variant', () => {
      renderWithPermission(
        <CreateButton service={BareMetalInstances} variant="secondary">
          Create item
        </CreateButton>,
      );

      expect(screen.getByRole('button', { name: 'Create item' })).toHaveClass('pf-m-secondary');
    });

    it('keeps the button inoperable when create permission is denied', async () => {
      const onClick = vi.fn();
      const { queryClient, user } = renderWithPermission(
        <CreateButton service={BareMetalInstances} onClick={onClick}>
          Create item
        </CreateButton>,
        false,
      );

      const button = screen.getByRole('button', { name: 'Create item' });
      await waitFor(() =>
        expect(
          queryClient.getQueryState(['permissions', BareMetalInstances.typeName, 'Create'])?.status,
        ).toBe('success'),
      );
      expect(button).toHaveAttribute('aria-disabled', 'true');
      expect(button).not.toBeDisabled();
      await user.click(button);

      expect(onClick).not.toHaveBeenCalled();
      expect(screen.queryByRole('tooltip')).not.toBeInTheDocument();
    });

    it('keeps a permission-check error focusable and exposes its message in a tooltip', async () => {
      const checkError = new ConnectError('permission check failed', Code.InvalidArgument);
      const transport = createRouterTransport((router) => {
        router.service(SelfSubjectAccessReviews, {
          create: () => {
            throw checkError;
          },
        });
      });
      const onClick = vi.fn();
      const onKeyPress = vi.fn();
      const { queryClient, user } = renderWithTransport(
        <CreateButton service={BareMetalInstances} onClick={onClick} onKeyPress={onKeyPress}>
          Create item
        </CreateButton>,
        transport,
      );

      await waitFor(() =>
        expect(
          queryClient.getQueryState(['permissions', BareMetalInstances.typeName, 'Create'])?.status,
        ).toBe('error'),
      );
      const button = screen.getByRole('button', { name: 'Create item' });

      expect(button).toHaveAttribute('aria-disabled', 'true');
      expect(button).toHaveClass('pf-m-aria-disabled');
      expect(button).not.toBeDisabled();
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();

      await user.tab();
      expect(button).toHaveFocus();
      const tooltip = await screen.findByRole('tooltip');
      expect(tooltip).toHaveTextContent(
        /^Failed to check create permissions\s*permission check failed$/,
      );
      expect(button).toHaveAttribute('aria-describedby', tooltip.id);
      expect(button).toHaveAccessibleDescription(
        /Failed to check create permissions\s*permission check failed/,
      );

      await user.click(button);
      await user.keyboard('{Enter}');
      expect(onClick).not.toHaveBeenCalled();
      expect(onKeyPress).not.toHaveBeenCalled();
    });

    it('keeps the button inoperable while the permission check is loading', async () => {
      let resolveResponse: (() => void) | undefined;
      const transport = createRouterTransport((router) => {
        router.service(SelfSubjectAccessReviews, {
          create: () =>
            new Promise((resolve) => {
              resolveResponse = () => resolve({ object: { status: { allowed: true } } });
            }),
        });
      });
      const onClick = vi.fn();
      const { user } = renderWithTransport(
        <CreateButton service={BareMetalInstances} onClick={onClick}>
          Create item
        </CreateButton>,
        transport,
      );
      const button = screen.getByRole('button', { name: 'Create item' });

      expect(button).toHaveAttribute('aria-disabled', 'true');
      expect(button).not.toBeDisabled();
      await user.click(button);
      expect(onClick).not.toHaveBeenCalled();

      await waitFor(() => expect(resolveResponse).toBeDefined());
      act(() => resolveResponse?.());
      await waitFor(() => expect(button).not.toHaveAttribute('aria-disabled'));
      await user.click(button);

      expect(onClick).toHaveBeenCalledTimes(1);
    });

    it('allows an admin to use private create actions without an SSAR request', () => {
      mockSession.role = 'admin';
      let requests = 0;
      const transport = createRouterTransport((router) => {
        router.service(SelfSubjectAccessReviews, {
          create: () => {
            requests += 1;
            return { object: { status: { allowed: false } } };
          },
        });
      });

      renderWithTransport(
        <CreateButton service={StorageBackends}>Create item</CreateButton>,
        transport,
      );

      expect(screen.getByRole('button', { name: 'Create item' })).not.toHaveAttribute(
        'aria-disabled',
      );
      expect(requests).toBe(0);
    });

    it('keeps a private create action unavailable for non-admins without SSAR', () => {
      let requests = 0;
      const transport = createRouterTransport((router) => {
        router.service(SelfSubjectAccessReviews, {
          create: () => {
            requests += 1;
            return { object: { status: { allowed: true } } };
          },
        });
      });

      renderWithTransport(
        <CreateButton service={StorageBackends}>Create item</CreateButton>,
        transport,
      );

      expect(screen.getByRole('button', { name: 'Create item' })).toHaveAttribute(
        'aria-disabled',
        'true',
      );
      expect(screen.queryByRole('tooltip')).not.toBeInTheDocument();
      expect(requests).toBe(0);
    });

    it('keeps a caller-disabled action inoperable after permission is allowed', async () => {
      const onClick = vi.fn();
      const { queryClient, user } = renderWithPermission(
        <CreateButton service={BareMetalInstances} isDisabled onClick={onClick}>
          Create item
        </CreateButton>,
      );

      await waitFor(() =>
        expect(
          queryClient.getQueryState(['permissions', BareMetalInstances.typeName, 'Create'])?.status,
        ).toBe('success'),
      );
      const button = screen.getByRole('button', { name: 'Create item' });
      expect(button).toHaveAttribute('aria-disabled', 'true');
      expect(button).not.toBeDisabled();
      await user.click(button);
      expect(onClick).not.toHaveBeenCalled();
      expect(screen.queryByRole('tooltip')).not.toBeInTheDocument();
    });
  });

  describe('user interactions', () => {
    it('calls onClick when used as a button', async () => {
      const onClick = vi.fn();

      const { user } = renderWithPermission(
        <CreateButton service={BareMetalInstances} onClick={onClick}>
          Create item
        </CreateButton>,
      );

      await waitFor(() =>
        expect(screen.getByRole('button', { name: 'Create item' })).not.toHaveAttribute(
          'aria-disabled',
        ),
      );
      await user.click(screen.getByRole('button', { name: 'Create item' }));

      expect(onClick).toHaveBeenCalledTimes(1);
    });

    it('lets the actual create operation surface PermissionDenied after SSAR allows it', async () => {
      let permissionChecks = 0;
      let createAttempts = 0;
      const transport = createRouterTransport((router) => {
        router.service(SelfSubjectAccessReviews, {
          create: () => {
            permissionChecks += 1;
            return { object: { status: { allowed: true } } };
          },
        });
        router.service(BareMetalInstances, {
          create: () => {
            createAttempts += 1;
            throw new ConnectError('create not permitted', Code.PermissionDenied);
          },
        });
      });
      const { user } = renderWithTransport(<CreateOperationConsumer />, transport);

      const button = screen.getByRole('button', { name: 'Create item' });
      await waitFor(() => expect(button).not.toHaveAttribute('aria-disabled'));
      await user.click(button);

      expect(await screen.findByRole('alert')).toHaveTextContent('create not permitted');
      expect(permissionChecks).toBe(1);
      expect(createAttempts).toBe(1);
    });
  });

  describe('when to is provided', () => {
    it('renders as a link to the given route', () => {
      renderWithPermission(
        <MemoryRouter>
          <CreateButton service={BareMetalInstances} to="/items/create">
            Create item
          </CreateButton>
        </MemoryRouter>,
      );

      const link = screen.getByRole('link', { name: 'Create item' });
      expect(link).toHaveAttribute('href', '/items/create');
      expect(link).toHaveClass('pf-m-primary');
      expect(link.querySelector('.pf-v6-c-button__icon')).not.toBeNull();
      expect(screen.queryByRole('button', { name: 'Create item' })).not.toBeInTheDocument();
    });

    it('navigates to the given route when clicked', async () => {
      const { user } = renderWithPermission(
        <MemoryRouter initialEntries={['/items']}>
          <Routes>
            <Route
              path="/items"
              element={
                <CreateButton service={BareMetalInstances} to="/items/create">
                  Create item
                </CreateButton>
              }
            />
            <Route path="/items/create" element={<h1>Create page</h1>} />
          </Routes>
        </MemoryRouter>,
      );

      await waitFor(() =>
        expect(screen.getByRole('link', { name: 'Create item' })).not.toHaveAttribute(
          'aria-disabled',
          'true',
        ),
      );
      await user.click(screen.getByRole('link', { name: 'Create item' }));

      expect(screen.getByRole('heading', { name: 'Create page' })).toBeInTheDocument();
    });

    it('marks the link as disabled when create permission is denied', async () => {
      const { queryClient } = renderWithPermission(
        <MemoryRouter>
          <CreateButton service={BareMetalInstances} to="/items/create">
            Create item
          </CreateButton>
        </MemoryRouter>,
        false,
      );

      await waitFor(() =>
        expect(
          queryClient.getQueryState(['permissions', BareMetalInstances.typeName, 'Create'])?.status,
        ).toBe('success'),
      );
      expect(screen.getByRole('link', { name: 'Create item' })).toHaveAttribute(
        'aria-disabled',
        'true',
      );
      expect(screen.queryByRole('tooltip')).not.toBeInTheDocument();
    });

    it('keeps an errored create link focusable without navigating', async () => {
      const checkError = new ConnectError('permission check failed', Code.InvalidArgument);
      const transport = createRouterTransport((router) => {
        router.service(SelfSubjectAccessReviews, {
          create: () => {
            throw checkError;
          },
        });
      });
      const onClick = vi.fn();
      const { queryClient, user } = renderWithTransport(
        <MemoryRouter initialEntries={['/items']}>
          <Routes>
            <Route
              path="/items"
              element={
                <>
                  <h1>Items</h1>
                  <CreateButton service={BareMetalInstances} to="/items/create" onClick={onClick}>
                    Create item
                  </CreateButton>
                </>
              }
            />
            <Route path="/items/create" element={<h1>Create page</h1>} />
          </Routes>
        </MemoryRouter>,
        transport,
      );

      await waitFor(() =>
        expect(
          queryClient.getQueryState(['permissions', BareMetalInstances.typeName, 'Create'])?.status,
        ).toBe('error'),
      );
      const link = screen.getByRole('link', { name: 'Create item' });
      expect(link).toHaveAttribute('aria-disabled', 'true');
      expect(link).toHaveClass('pf-m-aria-disabled');
      expect(link).not.toHaveAttribute('tabindex', '-1');

      await user.tab();
      expect(link).toHaveFocus();
      const tooltip = await screen.findByRole('tooltip');
      expect(tooltip).toHaveTextContent(
        /^Failed to check create permissions\s*permission check failed$/,
      );
      expect(link).toHaveAttribute('aria-describedby', tooltip.id);
      expect(link).toHaveAccessibleDescription(
        /Failed to check create permissions\s*permission check failed/,
      );

      await user.click(link);
      await user.keyboard('{Enter}');
      expect(onClick).not.toHaveBeenCalled();
      expect(screen.getByRole('heading', { name: 'Items' })).toBeInTheDocument();
      expect(screen.queryByRole('heading', { name: 'Create page' })).not.toBeInTheDocument();
    });
  });
});
