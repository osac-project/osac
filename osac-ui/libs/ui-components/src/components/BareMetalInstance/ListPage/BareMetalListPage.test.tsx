import { create } from '@bufbuild/protobuf';
import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  BareMetalInstanceCatalogItems,
  BareMetalInstanceRunStrategy,
  BareMetalInstanceState,
  BareMetalInstanceTypes,
  BareMetalInstances,
  DiskImageLifecycle,
  DiskImageSchema,
  DiskImages,
} from '@osac/types';
import { buildBareMetalListFilter } from '@osac/ui-components/components/BareMetalInstance/ListPage/baremetal-instance-list-filter';
import { wrapWithAuthInterceptor } from '@osac/ui-components/test-utils/createMockConnectTransport';
import { renderWithProviders } from '@osac/ui-components/test-utils/TestProviders';

import { BareMetalListPage } from './BareMetalListPage';

const setProjects = vi.fn();

vi.mock('@osac/ui-components/hooks/use-session.tsx', () => ({
  useSession: vi.fn(() => ({
    role: 'tenant-user',
    username: 'test-user',
    tenantId: 'test-tenant',
    projects: [],
    setProjects,
    userTheme: 'system',
    resolvedTheme: 'light',
    setUserTheme: vi.fn(),
    userContrast: 'system',
    resolvedContrast: 'normal',
    setUserContrast: vi.fn(),
  })),
  PROJECT_FILTER_PARAM: 'project',
  getProjectFilterStorageKey: (username: string) => `osac/project-filter/${username}`,
}));

const instance = {
  id: 'bmi-1',
  metadata: { name: 'worker-01', project: 'default' },
  spec: {
    catalogItem: { id: 'catalog-1', name: 'RHEL 9 bare metal', project: 'default', shared: false },
    diskImage: { name: 'RHEL 9.4' },
    runStrategy: BareMetalInstanceRunStrategy.ALWAYS,
    restartTrigger: 0n,
  },
  status: { state: BareMetalInstanceState.RUNNING },
};

const fedoraInstance = {
  ...instance,
  id: 'bmi-2',
  metadata: { name: 'worker-02', project: 'default' },
  spec: {
    ...instance.spec,
    diskImage: { name: 'Fedora 41' },
  },
};

const diskImages = [
  create(DiskImageSchema, {
    id: 'disk-rhel',
    metadata: { name: 'RHEL 9.4' },
    spec: { sourceRef: 'example/rhel', lifecycle: DiskImageLifecycle.AVAILABLE },
  }),
  create(DiskImageSchema, {
    id: 'disk-fedora',
    metadata: { name: 'Fedora 41' },
    spec: { sourceRef: 'example/fedora', lifecycle: DiskImageLifecycle.AVAILABLE },
  }),
];

const createTransport = (items = [instance, fedoraInstance]) => {
  let instanceListFilter: string | undefined;

  const transport = wrapWithAuthInterceptor(
    createRouterTransport((router) => {
      router.service(BareMetalInstances, {
        list: (req) => {
          const limit = Number(req.limit);
          if (limit === 0) {
            return { items: [], total: items.length };
          }
          instanceListFilter = req.filter;
          return { items, total: items.length };
        },
        get: ({ id }) => ({ object: items.find((item) => item.id === id) ?? instance }),
      });
      router.service(DiskImages, {
        list: () => ({ items: diskImages, total: diskImages.length }),
        get: ({ id }) => ({ object: diskImages.find((item) => item.id === id) ?? diskImages[0] }),
      });
      router.service(BareMetalInstanceTypes, {
        list: () => ({ items: [], total: 0 }),
        get: () => ({ object: undefined }),
      });
      router.service(BareMetalInstanceCatalogItems, {
        list: () => ({ items: [], total: 0 }),
        get: () => ({ object: undefined }),
      });
    }),
  );

  return { transport, getInstanceListFilter: () => instanceListFilter };
};

const createTransportWithDiskImageListError = (items = [instance, fedoraInstance]) => {
  const transport = wrapWithAuthInterceptor(
    createRouterTransport((router) => {
      router.service(BareMetalInstances, {
        list: (req) => {
          const limit = Number(req.limit);
          if (limit === 0) {
            return { items: [], total: items.length };
          }
          return { items, total: items.length };
        },
        get: ({ id }) => ({ object: items.find((item) => item.id === id) ?? instance }),
      });
      router.service(DiskImages, {
        list: () => {
          throw new ConnectError('disk images unavailable', Code.Unavailable);
        },
        get: () => {
          throw new ConnectError('disk images unavailable', Code.Unavailable);
        },
      });
      router.service(BareMetalInstanceTypes, {
        list: () => ({ items: [], total: 0 }),
        get: () => ({ object: undefined }),
      });
      router.service(BareMetalInstanceCatalogItems, {
        list: () => ({ items: [], total: 0 }),
        get: () => ({ object: undefined }),
      });
    }),
  );

  return { transport };
};

describe('BareMetalListPage', () => {
  beforeEach(() => {
    localStorage.clear();
    setProjects.mockClear();
  });

  it('loads instances when spec filter options fail and no hardware spec filters are active', async () => {
    const { transport } = createTransportWithDiskImageListError();
    const { user } = renderWithProviders(<BareMetalListPage />, { transport });

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'worker-01' })).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Filter bare metal by specs' }));

    expect(screen.getByText('Could not load spec filter options')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Bare Metal' })).toBeInTheDocument();
  });

  it('sends a CEL filter when filtering bare metal instances by specs', async () => {
    const { transport, getInstanceListFilter } = createTransport();
    const { user } = renderWithProviders(<BareMetalListPage />, { transport });

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'worker-01' })).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Filter bare metal by specs' }));
    await user.click(screen.getByRole('checkbox', { name: 'Fedora 41' }));

    const expected = buildBareMetalListFilter({
      specs: { diskImage: ['Fedora 41'], gpu: [], ram: [], cpu: [] },
    });

    await waitFor(() => {
      expect(getInstanceListFilter()).toBe(expected);
    });
  });

  it('sends a CEL filter when filtering bare metal instances by power state', async () => {
    const { transport, getInstanceListFilter } = createTransport([instance]);
    const { user } = renderWithProviders(<BareMetalListPage />, { transport });

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'worker-01' })).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Filter bare metal by power state' }));
    await user.click(screen.getByRole('option', { name: 'Stopped' }));

    const expected = buildBareMetalListFilter({ powerState: 'stopped' });

    await waitFor(() => {
      expect(getInstanceListFilter()).toBe(expected);
    });
  });

  it('sends a CEL filter when filtering bare metal instances by search', async () => {
    const { transport, getInstanceListFilter } = createTransport();
    const { user } = renderWithProviders(<BareMetalListPage />, { transport });

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'worker-01' })).toBeInTheDocument();
    });

    const searchInput = screen.getByRole('textbox', {
      name: 'Filter bare metal instances by name',
    });
    await user.type(searchInput, 'worker-02');

    const expected = buildBareMetalListFilter({ search: 'worker-02' });

    await waitFor(() => {
      expect(getInstanceListFilter()).toBe(expected);
    });
  });

  it('clears search and project filters together', async () => {
    const { transport, getInstanceListFilter } = createTransport([instance]);
    const { user } = renderWithProviders(<BareMetalListPage />, {
      transport,
      routerEntries: ['/?search=no-such-instance'],
    });

    await waitFor(() => {
      expect(getInstanceListFilter()).toBe(
        buildBareMetalListFilter({ search: 'no-such-instance' }),
      );
    });

    expect(
      screen.getByRole('textbox', { name: 'Filter bare metal instances by name' }),
    ).toHaveValue('no-such-instance');

    await user.click(screen.getByRole('button', { name: 'Clear all filters' }));

    await waitFor(() => {
      expect(
        screen.getByRole('textbox', { name: 'Filter bare metal instances by name' }),
      ).toHaveValue('');
      expect(getInstanceListFilter()).toBeUndefined();
    });

    expect(setProjects).toHaveBeenCalledWith([]);
  });
});
