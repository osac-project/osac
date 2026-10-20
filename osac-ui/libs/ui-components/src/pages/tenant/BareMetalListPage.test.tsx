import { create } from '@bufbuild/protobuf';
import { createRouterTransport } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  BareMetalInstanceRunStrategy,
  BareMetalInstanceState,
  BareMetalInstanceTypeSchema,
  BareMetalInstanceTypes,
  BareMetalInstances,
  DiskImageLifecycle,
  DiskImageSchema,
  DiskImages,
  ExternalIPAttachments,
  ProjectSchema,
  Projects,
} from '@osac/types';
import { buildBareMetalListFilter } from '@osac/ui-components/components/BareMetalInstance/ListPage/baremetal-instance-list-filter';
import { wrapWithAuthInterceptor } from '@osac/ui-components/test-utils/createMockConnectTransport';
import { renderWithProviders } from '@osac/ui-components/test-utils/TestProviders';

import { BareMetalListPage } from './BareMetalListPage';

vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) =>
  window.setTimeout(() => callback(0), 0),
);

const myProject = create(ProjectSchema, {
  id: 'project-my-project',
  metadata: { name: 'my-project' },
});

const tenantSession = { role: 'tenant-user' as const };

const instance = {
  id: 'bmi-1',
  metadata: { name: 'worker-01', project: 'default' },
  spec: {
    catalogItem: { id: 'catalog-1', name: 'RHEL 9 bare metal', project: 'default', shared: false },
    instanceType: { id: 'gpu-large', name: 'gpu-large', project: 'default', shared: false },
    diskImage: { id: 'disk-rhel', name: 'RHEL 9.4' },
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
    diskImage: { id: 'disk-fedora', name: 'Fedora 41' },
  },
};

const gpuLargeInstanceType = create(BareMetalInstanceTypeSchema, {
  id: 'gpu-large',
  metadata: { name: 'gpu-large' },
  spec: {
    description: 'GPU-enabled bare metal host',
    hardware: {
      cpu: { cores: 64, architecture: 'x86_64', model: 'EPYC', threadsPerCore: 1 },
      memory: { totalGb: 512n, type: 'DDR5' },
      accelerators: [{ type: 'GPU', model: 'A100', vendor: 'NVIDIA', memoryGb: 80 }],
      disks: [],
      networkPorts: [],
      capabilities: {},
    },
  },
});

const rhelDiskImage = create(DiskImageSchema, {
  id: 'disk-rhel',
  metadata: { name: 'RHEL 9.4' },
  spec: { lifecycle: DiskImageLifecycle.AVAILABLE, sourceRef: 'quay.io/rhel:latest' },
});

const fedoraDiskImage = create(DiskImageSchema, {
  id: 'disk-fedora',
  metadata: { name: 'Fedora 41' },
  spec: { lifecycle: DiskImageLifecycle.AVAILABLE, sourceRef: 'quay.io/fedora:latest' },
});

const diskImages = [rhelDiskImage, fedoraDiskImage];

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
      router.service(BareMetalInstanceTypes, {
        list: () => ({ items: [gpuLargeInstanceType], total: 1 }),
        get: () => ({ object: gpuLargeInstanceType }),
      });
      router.service(DiskImages, {
        list: () => ({ items: diskImages, total: diskImages.length }),
        get: ({ id }) => ({ object: diskImages.find((diskImage) => diskImage.id === id) }),
      });
      router.service(Projects, {
        list: () => ({ items: [myProject], total: 1 }),
        get: () => ({ object: myProject }),
      });
      router.service(ExternalIPAttachments, {
        list: () => ({ items: [], total: 0 }),
      });
    }),
  );

  return { transport, getInstanceListFilter: () => instanceListFilter };
};

describe('BareMetalListPage', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('renders instance type from spec.instance_type', async () => {
    const { transport } = createTransport([instance]);
    renderWithProviders(<BareMetalListPage />, { transport, session: tenantSession });

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'worker-01' })).toBeInTheDocument();
      expect(screen.getByText('gpu-large')).toBeInTheDocument();
    });
  });

  it('sends a CEL filter when filtering bare metal instances by disk image', async () => {
    const { transport, getInstanceListFilter } = createTransport();
    const { user } = renderWithProviders(<BareMetalListPage />, {
      transport,
      session: tenantSession,
    });

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'worker-01' })).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Filter bare metal by disk image' }));
    await user.click(screen.getByRole('option', { name: 'RHEL 9.4' }));

    const expected = buildBareMetalListFilter({ diskImage: 'RHEL 9.4' });

    await waitFor(() => {
      expect(getInstanceListFilter()).toBe(expected);
    });
  });

  it('sends a CEL filter when filtering bare metal instances by instance type', async () => {
    const { transport, getInstanceListFilter } = createTransport();
    const { user } = renderWithProviders(<BareMetalListPage />, {
      transport,
      session: tenantSession,
    });

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'worker-01' })).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Filter bare metal by instance type' }));
    await user.click(screen.getByRole('option', { name: 'gpu-large' }));

    const expected = buildBareMetalListFilter({ instanceType: 'gpu-large' });

    await waitFor(() => {
      expect(getInstanceListFilter()).toBe(expected);
    });
  });

  it('sends a CEL filter when filtering bare metal instances by power state', async () => {
    const { transport, getInstanceListFilter } = createTransport([instance]);
    const { user } = renderWithProviders(<BareMetalListPage />, {
      transport,
      session: tenantSession,
    });

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
    const { user } = renderWithProviders(<BareMetalListPage />, {
      transport,
      session: tenantSession,
    });

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
      routerEntries: ['/?project=my-project&search=no-such-instance'],
      session: tenantSession,
    });

    await waitFor(() => {
      expect(
        screen.getByRole('textbox', { name: 'Filter bare metal instances by name' }),
      ).toHaveValue('no-such-instance');
      expect(screen.getByRole('button', { name: 'my-project' })).toBeInTheDocument();
    });

    const clearFiltersButtons = await screen.findAllByRole('button', { name: 'Clear all filters' });
    await user.click(clearFiltersButtons[0]);

    await waitFor(() => {
      expect(
        screen.getByRole('textbox', { name: 'Filter bare metal instances by name' }),
      ).toHaveValue('');
    });
    await waitFor(() => {
      expect(getInstanceListFilter()).toBeUndefined();
    });

    expect(screen.getByRole('button', { name: 'All projects' })).toBeInTheDocument();
  });
});
