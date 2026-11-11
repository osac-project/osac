import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Tenant, TenantsListResponse } from '@osac/types/private';
import { TenantState } from '@osac/types/private';
import { buildTenantListFilter } from '@osac/ui-components/components/Tenant/utils';
import { getViewTypePrefKey } from '@osac/ui-components/hooks/use-view-type';
import { mockQueryResult } from '@osac/ui-components/test-utils/query';

import TenantListPage from './TenantListPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) =>
  window.setTimeout(() => callback(0), 0),
);

const makeTenant = (id: string, name: string, state?: TenantState) =>
  ({
    id,
    metadata: {
      name,
      creationTimestamp: { seconds: BigInt(1717000000), nanos: 0 },
    },
    spec: { domains: [`${name}.example.com`] },
    status: state !== undefined ? { state } : undefined,
  }) as Tenant;

const makeListResponse = (items: Tenant[], total?: number): TenantsListResponse =>
  ({
    items,
    total: total ?? items.length,
  }) as TenantsListResponse;

vi.mock('../../api/use-resource', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/use-resource')>();
  return { ...actual, useListResource: vi.fn() };
});

const { useListResource } = await import('../../api/use-resource');

const mockListResource = (items: Tenant[], total?: number) => {
  vi.mocked(useListResource).mockImplementation((_service, request) => {
    const limit = request?.limit;
    if (limit === 0) {
      return mockQueryResult<TenantsListResponse>({
        data: makeListResponse([], total ?? items.length),
        isLoading: false,
        error: null,
      });
    }
    return mockQueryResult<TenantsListResponse>({
      data: makeListResponse(items, total ?? items.length),
      isLoading: false,
      error: null,
    });
  });
};

const renderPage = (routerEntries?: string[]) =>
  renderWithProviders(<TenantListPage />, { routerEntries });

const getTenantListFilter = (): string | undefined => {
  const listCalls = vi.mocked(useListResource).mock.calls.filter(([, request]) => {
    const { limit } = request as { limit?: number };
    return limit !== 0;
  });
  const { filter } = (listCalls.at(-1)?.[1] ?? {}) as { filter?: string };
  return filter;
};

describe('TenantListPage', () => {
  beforeEach(() => {
    localStorage.clear();
    mockListResource([
      makeTenant('t-1', 'acme', TenantState.SYNCED),
      makeTenant('t-2', 'globex', TenantState.PENDING),
    ]);
  });

  it('renders the page title', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Tenants' })).toBeInTheDocument();
    });
    expect(screen.getByRole('link', { name: 'Create tenant' })).toHaveAttribute(
      'href',
      '/admin/tenants/create',
    );
  });

  it('renders tenant rows with names', async () => {
    localStorage.setItem(getViewTypePrefKey('tenants'), 'list');
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('acme')).toBeInTheDocument();
    });
    expect(screen.getByText('globex')).toBeInTheDocument();
    expect(screen.getByText('2 tenants')).toBeInTheDocument();
  });

  it('renders status labels for each tenant', async () => {
    localStorage.setItem(getViewTypePrefKey('tenants'), 'list');
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('Synced')).toBeInTheDocument();
    });
    expect(screen.getByText('Pending')).toBeInTheDocument();
  });

  it('renders primary domain for each tenant', async () => {
    localStorage.setItem(getViewTypePrefKey('tenants'), 'list');
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('acme.example.com')).toBeInTheDocument();
    });
    expect(screen.getByText('globex.example.com')).toBeInTheDocument();
  });

  it('shows an empty state when no tenants are returned', async () => {
    mockListResource([], 0);

    renderPage();

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: 'No tenants found', level: 2 }),
      ).toBeInTheDocument();
    });
    expect(screen.getByText('No tenants are available yet.')).toBeInTheDocument();
  });

  it('shows a filter-specific empty state when the search matches nothing', async () => {
    mockListResource([], 2);

    renderPage(['/?search=no-such-tenant']);

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: 'No tenants match your filters', level: 2 }),
      ).toBeInTheDocument();
    });
    expect(screen.getByText('Try a different state or search term.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Clear all filters' })).toBeInTheDocument();
  });

  it('renders the name search input', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByPlaceholderText('Search tenants')).toBeInTheDocument();
    });
  });

  it('renders the status filter toggle', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'All states' })).toBeInTheDocument();
    });
  });

  it('renders the list and card view switcher', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Card view' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'List view' })).toBeInTheDocument();
    });
  });

  describe('filters', () => {
    it('sends a CEL filter when filtering tenants by status', async () => {
      const { user } = renderPage();

      await screen.findByText('acme');

      await user.click(screen.getByRole('button', { name: 'All states' }));
      await user.click(screen.getByRole('checkbox', { name: 'Pending' }));

      const expected = buildTenantListFilter([TenantState.PENDING], '');

      await waitFor(() => {
        expect(getTenantListFilter()).toBe(expected);
      });
    });

    it('sends a CEL filter when searching tenants by name', async () => {
      const { user } = renderPage();

      await screen.findByText('acme');

      await user.type(screen.getByRole('textbox', { name: 'Search tenants' }), 'acme');

      const expected = buildTenantListFilter([], 'acme');

      await waitFor(() => {
        expect(getTenantListFilter()).toBe(expected);
      });
    });

    it('applies status and search filters from URL search params', async () => {
      renderPage(['/?state=synced&search=acme']);

      await waitFor(() => {
        expect(screen.getByRole('textbox', { name: 'Search tenants' })).toHaveValue('acme');
        expect(screen.getByRole('button', { name: 'Synced' })).toBeInTheDocument();
      });

      const expected = buildTenantListFilter([TenantState.SYNCED], 'acme');

      await waitFor(() => {
        expect(getTenantListFilter()).toBe(expected);
      });
    });

    it('clears search and status filters together', async () => {
      const { user } = renderPage(['/?search=no-such&state=pending']);

      await waitFor(() => {
        expect(screen.getByRole('textbox', { name: 'Search tenants' })).toHaveValue('no-such');
        expect(screen.getByRole('button', { name: 'Pending' })).toBeInTheDocument();
      });

      const clearFiltersButtons = await screen.findAllByRole('button', {
        name: 'Clear all filters',
      });
      await user.click(clearFiltersButtons[0]);

      await waitFor(() => {
        expect(screen.getByRole('textbox', { name: 'Search tenants' })).toHaveValue('');
      });
      await waitFor(() => {
        expect(getTenantListFilter()).toBeUndefined();
      });

      expect(screen.getByRole('button', { name: 'All states' })).toBeInTheDocument();
    });
  });
});
