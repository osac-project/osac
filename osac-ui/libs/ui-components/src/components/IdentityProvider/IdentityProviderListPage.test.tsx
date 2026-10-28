import { create } from '@bufbuild/protobuf';
import { screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  type IdentityProvider,
  IdentityProviderPhase,
  IdentityProviderSchema,
  type IdentityProvidersListResponse,
  IdentityProvidersListResponseSchema,
} from '@osac/types';
import { buildIdentityProviderListFilter } from '@osac/ui-components/components/IdentityProvider/utils';
import { getViewTypePrefKey } from '@osac/ui-components/hooks/use-view-type';
import { mockQueryResult } from '@osac/ui-components/test-utils/query';

import IdentityProviderListPage from './IdentityProviderListPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) =>
  window.setTimeout(() => callback(0), 0),
);

const makeIdentityProvider = (
  id: string,
  title: string,
  phase: IdentityProviderPhase = IdentityProviderPhase.READY,
  issuer?: string,
  enabled = true,
): IdentityProvider =>
  create(IdentityProviderSchema, {
    id,
    metadata: {
      creationTimestamp: { seconds: BigInt(1717000000), nanos: 0 },
    },
    spec: {
      title,
      enabled,
      config: { case: 'oidc', value: { issuer: issuer ?? `https://idp.example.com/${id}` } },
    },
    status: { phase, message: '' },
  });

const makeListResponse = (
  items: IdentityProvider[],
  total?: number,
): IdentityProvidersListResponse =>
  create(IdentityProvidersListResponseSchema, { items, total: total ?? items.length });

vi.mock('../../api/use-resource', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/use-resource')>();
  return { ...actual, useListResource: vi.fn() };
});

const { useListResource } = await import('../../api/use-resource');

const mockListResource = (items: IdentityProvider[], total?: number) => {
  vi.mocked(useListResource).mockImplementation((_service, request) => {
    const limit = request?.limit;
    if (limit === 0) {
      return mockQueryResult<IdentityProvidersListResponse>({
        data: makeListResponse([], total ?? items.length),
        isLoading: false,
        error: null,
      });
    }
    return mockQueryResult<IdentityProvidersListResponse>({
      data: makeListResponse(items, total ?? items.length),
      isLoading: false,
      error: null,
    });
  });
};

const renderPage = (routerEntries?: string[]) =>
  renderWithProviders(<IdentityProviderListPage />, {
    session: { role: 'tenant-idp-manager' },
    routerEntries,
  });

const getIdentityProviderListFilter = (): string | undefined => {
  const listCalls = vi.mocked(useListResource).mock.calls.filter(([, request]) => {
    const { limit } = request as { limit?: number };
    return limit !== 0;
  });
  const { filter } = (listCalls.at(-1)?.[1] ?? {}) as { filter?: string };
  return filter;
};

describe('IdentityProviderListPage', () => {
  beforeEach(() => {
    localStorage.clear();
    mockListResource([
      makeIdentityProvider('idp-1', 'Corporate OIDC', IdentityProviderPhase.READY),
      makeIdentityProvider('idp-2', 'GitHub SSO', IdentityProviderPhase.ERROR),
    ]);
  });

  it('renders the page title', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Identity providers' })).toBeInTheDocument();
    });
    expect(screen.getByRole('link', { name: 'Create identity provider' })).toHaveAttribute(
      'href',
      '/tenant/identity-provider/create',
    );
  });

  it('renders identity provider rows with titles', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('Corporate OIDC')).toBeInTheDocument();
    });
    expect(screen.getByText('GitHub SSO')).toBeInTheDocument();
    expect(screen.getByText('2 identity providers')).toBeInTheDocument();
  });

  it('renders status labels for each identity provider', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('Ready')).toBeInTheDocument();
    });
    expect(screen.getByText('Error')).toBeInTheDocument();
  });

  it('renders type column showing OIDC', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getAllByText('OpenID Connect (OIDC)')).toHaveLength(2);
    });
  });

  it('renders issuer URL for each identity provider', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('https://idp.example.com/idp-1')).toBeInTheDocument();
    });
    expect(screen.getByText('https://idp.example.com/idp-2')).toBeInTheDocument();
  });

  it('shows an empty state when no identity providers are returned', async () => {
    mockListResource([], 0);

    renderPage();

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: 'No identity providers found', level: 2 }),
      ).toBeInTheDocument();
    });
    expect(screen.getByText('No identity providers are available yet.')).toBeInTheDocument();
  });

  it('shows a filter-specific empty state when the search matches nothing', async () => {
    mockListResource([], 2);

    renderPage(['/?search=no-such-idp']);

    await waitFor(() => {
      expect(
        screen.getByRole('heading', {
          name: 'No identity providers match your filters',
          level: 2,
        }),
      ).toBeInTheDocument();
    });
    expect(
      screen.getByText('Try a different state, enablement, or search term.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Clear all filters' })).toBeInTheDocument();
  });

  it('renders the name search input', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByPlaceholderText('Search identity providers')).toBeInTheDocument();
    });
  });

  it('renders the status filter toggle', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'All states' })).toBeInTheDocument();
    });
  });

  it('renders the enabled filter toggle', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'All enablements' })).toBeInTheDocument();
    });
  });

  it('renders enabled state for each identity provider', async () => {
    mockListResource([
      makeIdentityProvider('idp-1', 'Corporate OIDC', IdentityProviderPhase.READY, undefined, true),
      makeIdentityProvider('idp-2', 'Inactive SSO', IdentityProviderPhase.READY, undefined, false),
    ]);
    localStorage.setItem(getViewTypePrefKey('identity-providers'), 'list');

    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('grid', { name: 'Identity providers' })).toBeInTheDocument();
    });
    const corporateRow = screen.getByRole('row', { name: /Corporate OIDC/ });
    expect(within(corporateRow).getByRole('cell', { name: 'Enabled' })).toBeInTheDocument();

    const inactiveRow = screen.getByRole('row', { name: /Inactive SSO/ });
    expect(inactiveRow.querySelector('td[data-label="Enabled"]')).toHaveTextContent('Disabled');
  });

  it('renders the list and card view switcher', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Card view' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'List view' })).toBeInTheDocument();
    });
  });

  describe('filters', () => {
    it('sends a CEL filter when filtering identity providers by status', async () => {
      const { user } = renderPage();

      await screen.findByText('Corporate OIDC');

      await user.click(screen.getByRole('button', { name: 'All states' }));
      await user.click(screen.getByRole('checkbox', { name: 'Error' }));

      const expected = buildIdentityProviderListFilter(
        [IdentityProviderPhase.ERROR],
        undefined,
        '',
      );

      await waitFor(() => {
        expect(getIdentityProviderListFilter()).toBe(expected);
      });
    });

    it('sends a CEL filter when filtering identity providers by enabled state', async () => {
      const { user } = renderPage();

      await screen.findByText('Corporate OIDC');

      await user.click(screen.getByRole('button', { name: 'All enablements' }));
      await user.click(screen.getByRole('option', { name: 'Disabled' }));

      const expected = buildIdentityProviderListFilter([], false, '');

      await waitFor(() => {
        expect(getIdentityProviderListFilter()).toBe(expected);
      });
    });

    it('sends a CEL filter when searching identity providers by title', async () => {
      const { user } = renderPage();

      await screen.findByText('Corporate OIDC');

      await user.type(screen.getByRole('textbox', { name: 'Search identity providers' }), 'Corp');

      const expected = buildIdentityProviderListFilter([], undefined, 'Corp');

      await waitFor(() => {
        expect(getIdentityProviderListFilter()).toBe(expected);
      });
    });

    it('applies status and search filters from URL search params', async () => {
      renderPage(['/?state=ready&search=Corporate']);

      await waitFor(() => {
        expect(screen.getByRole('textbox', { name: 'Search identity providers' })).toHaveValue(
          'Corporate',
        );
        expect(screen.getByRole('button', { name: 'Ready' })).toBeInTheDocument();
      });

      const expected = buildIdentityProviderListFilter(
        [IdentityProviderPhase.READY],
        undefined,
        'Corporate',
      );

      await waitFor(() => {
        expect(getIdentityProviderListFilter()).toBe(expected);
      });
    });

    it('applies the enabled filter from URL search params', async () => {
      renderPage(['/?enabled=true']);

      await waitFor(() => {
        expect(screen.getByRole('button', { name: 'Enabled' })).toBeInTheDocument();
      });

      const expected = buildIdentityProviderListFilter([], true, '');

      await waitFor(() => {
        expect(getIdentityProviderListFilter()).toBe(expected);
      });
    });

    it('clears search, status, and enabled filters together', async () => {
      const { user } = renderPage(['/?search=no-such&state=ready&enabled=false']);

      await waitFor(() => {
        expect(screen.getByRole('textbox', { name: 'Search identity providers' })).toHaveValue(
          'no-such',
        );
        expect(screen.getByRole('button', { name: 'Ready' })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Disabled' })).toBeInTheDocument();
      });

      const clearFiltersButtons = await screen.findAllByRole('button', {
        name: 'Clear all filters',
      });
      await user.click(clearFiltersButtons[0]);

      await waitFor(() => {
        expect(screen.getByRole('textbox', { name: 'Search identity providers' })).toHaveValue('');
      });
      await waitFor(() => {
        expect(getIdentityProviderListFilter()).toBeUndefined();
      });

      expect(screen.getByRole('button', { name: 'All states' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'All enablements' })).toBeInTheDocument();
    });
  });
});
