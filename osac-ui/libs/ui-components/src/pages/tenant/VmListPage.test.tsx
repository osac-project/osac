import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { ComputeInstance, DiskImage, InstanceType } from '@osac/types';
import { ComputeInstanceState } from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';
import { useComputeInstances } from '@osac/ui-components/api/v1/compute-instance';
import { useDiskImages } from '@osac/ui-components/api/v1/disk-image';
import { buildVmListFilter } from '@osac/ui-components/components/vm/ListPage/vm-list-filter';
import { getViewTypePrefKey } from '@osac/ui-components/hooks/use-view-type';
import { mockQueryResult } from '@osac/ui-components/test-utils/query';

import { VmListPage } from './VmListPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

const VM_LIST_VIEW_PREF_KEY = getViewTypePrefKey('vm-list');

vi.mock('@osac/ui-components/api/v1/compute-instance', async (importOriginal) => {
  const actual =
    await importOriginal<typeof import('@osac/ui-components/api/v1/compute-instance')>();
  return {
    ...actual,
    useComputeInstances: vi.fn(),
  };
});

vi.mock('@osac/ui-components/api/use-resource', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@osac/ui-components/api/use-resource')>();
  return {
    ...actual,
    useListResource: vi.fn(),
  };
});

vi.mock('@osac/ui-components/api/v1/disk-image', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@osac/ui-components/api/v1/disk-image')>();
  return {
    ...actual,
    useDiskImages: vi.fn(),
  };
});

vi.mock('@osac/ui-components/components/vm/actions/VmActionButtons', () => ({
  default: () => <button type="button">Actions</button>,
}));

const vm = {
  id: 'vm-1',
  metadata: { name: 'web-01', project: 'dev-project' },
  spec: {
    instanceType: { id: 'standard-4-8', name: 'Standard 4 vCPU / 8 GiB' },
    diskImage: { id: 'img-1', name: 'rhel-9' },
  },
  status: {
    state: ComputeInstanceState.RUNNING,
    internalIpAddress: '10.0.0.5',
    externalIpAddress: '203.0.113.1',
  },
} as ComputeInstance;

const instanceTypes = [
  { id: 'standard-4-8', metadata: { name: 'Standard 4 vCPU / 8 GiB' } },
  { id: 'large-8-16', metadata: { name: 'Large 8 vCPU / 16 GiB' } },
] as InstanceType[];

const diskImages = [{ id: 'img-1', metadata: { name: 'rhel-9' } }] as DiskImage[];

const renderPage = (routerEntries: string[] = ['/']) =>
  renderWithProviders(<VmListPage />, { session: { role: 'tenant-user' }, routerEntries });

const mockVmList = (overrides: Partial<ReturnType<typeof useComputeInstances>> = {}) => {
  vi.mocked(useComputeInstances).mockReturnValue({
    vms: [vm],
    totalItems: 1,
    isLoading: false,
    error: null,
    ...overrides,
  });
};

describe('VmListPage', () => {
  beforeEach(() => {
    localStorage.removeItem(VM_LIST_VIEW_PREF_KEY);

    mockVmList();

    vi.mocked(useListResource).mockReturnValue(
      mockQueryResult({
        data: { items: instanceTypes },
        isLoading: false,
        error: null,
      }) as ReturnType<typeof useListResource>,
    );

    vi.mocked(useDiskImages).mockReturnValue(
      mockQueryResult({
        data: diskImages,
        isLoading: false,
        error: null,
      }),
    );
  });

  afterEach(() => {
    localStorage.removeItem(VM_LIST_VIEW_PREF_KEY);
  });

  describe('page chrome', () => {
    it('renders the title, description, and create action', async () => {
      renderPage();

      expect(await screen.findByRole('heading', { name: 'Virtual machines' })).toBeInTheDocument();
      expect(
        screen.getByText('Monitor and manage virtual machine instances across all projects.'),
      ).toBeInTheDocument();
      expect(screen.getByRole('link', { name: 'Create virtual machine' })).toHaveAttribute(
        'href',
        '/vms/create',
      );
    });

    it('shows the total VM count when every VM is visible', async () => {
      renderPage();

      expect(await screen.findByText('1 virtual machine')).toBeInTheDocument();
    });

    it('shows a partial count when the list is narrowed by filters', async () => {
      mockVmList({ vms: [vm], totalItems: 4 });

      renderPage();

      expect(await screen.findByText('1 of 4 virtual machines')).toBeInTheDocument();
    });
  });

  describe('loading and empty states', () => {
    it('shows a loading indicator while VMs load', async () => {
      mockVmList({ vms: [], totalItems: 0, isLoading: true });

      renderPage();

      expect(await screen.findByRole('heading', { name: 'Virtual machines' })).toBeInTheDocument();
      expect(screen.getAllByRole('progressbar').length).toBeGreaterThanOrEqual(1);
      expect(
        screen.getByRole('textbox', { name: 'Filter virtual machines by name' }),
      ).toBeEnabled();
    });

    it('shows the unfiltered empty state when no VMs exist', async () => {
      mockVmList({ vms: [], totalItems: 0 });

      renderPage();

      expect(
        await screen.findByRole('heading', { name: 'No virtual machines found' }),
      ).toBeInTheDocument();
      expect(screen.getByText('No virtual machines are available yet.')).toBeInTheDocument();
    });

    it('shows the filtered empty state when filters match nothing', async () => {
      mockVmList({ vms: [], totalItems: 3 });

      renderPage(['/?search=missing']);

      expect(
        await screen.findByRole('heading', { name: 'No virtual machines match your filters' }),
      ).toBeInTheDocument();
      expect(
        screen.getByText(
          /Try a different project, power state, instance type, disk image, or search term/i,
        ),
      ).toBeInTheDocument();
    });
  });

  describe('server-side filters', () => {
    it('sends a filter when searching by name', async () => {
      const user = userEvent.setup();

      renderPage();
      await screen.findByRole('link', { name: 'web-01' });

      await user.type(
        screen.getByRole('textbox', { name: 'Filter virtual machines by name' }),
        'web',
      );

      await waitFor(() => {
        expect(useComputeInstances).toHaveBeenLastCalledWith(buildVmListFilter({ search: 'web' }));
      });
    });

    it('sends a filter when filtering by power state', async () => {
      const user = userEvent.setup();

      renderPage();
      await screen.findByRole('link', { name: 'web-01' });

      await user.click(
        screen.getByRole('button', { name: 'Filter virtual machines by power state' }),
      );
      await user.click(
        within(screen.getByRole('listbox')).getByRole('option', { name: 'Running' }),
      );

      await waitFor(() => {
        expect(useComputeInstances).toHaveBeenLastCalledWith(
          buildVmListFilter({ powerState: 'running' }),
        );
      });
    });

    it('sends a filter when filtering by instance type', async () => {
      const user = userEvent.setup();

      renderPage();
      await screen.findByRole('link', { name: 'web-01' });

      await user.click(screen.getByRole('button', { name: 'Filter by instance type' }));
      await user.click(
        within(screen.getByRole('listbox')).getByRole('option', {
          name: 'Standard 4 vCPU / 8 GiB',
        }),
      );

      await waitFor(() => {
        expect(useComputeInstances).toHaveBeenLastCalledWith(
          buildVmListFilter({ instanceType: 'Standard 4 vCPU / 8 GiB' }),
        );
      });
    });

    it('sends a filter when filtering by disk image', async () => {
      const user = userEvent.setup();

      renderPage();
      await screen.findByRole('link', { name: 'web-01' });

      await user.click(screen.getByRole('button', { name: 'Filter by disk image' }));
      await user.click(within(screen.getByRole('listbox')).getByRole('option', { name: 'rhel-9' }));

      await waitFor(() => {
        expect(useComputeInstances).toHaveBeenLastCalledWith(
          buildVmListFilter({ diskImage: 'rhel-9' }),
        );
      });
    });

    it('clears toolbar filters from the list page toolbar', async () => {
      const user = userEvent.setup();

      mockVmList({ vms: [], totalItems: 3 });

      renderPage(['/?search=web']);
      await screen.findByRole('heading', { name: 'No virtual machines match your filters' });

      await user.click(screen.getAllByRole('button', { name: 'Clear all filters' })[0]);

      await waitFor(() => {
        expect(useComputeInstances).toHaveBeenLastCalledWith(undefined);
      });
    });
  });

  describe('view layout', () => {
    it('renders card layout by default', async () => {
      renderPage();

      await screen.findByRole('link', { name: 'web-01' });
      expect(screen.queryByRole('columnheader', { name: 'Name' })).not.toBeInTheDocument();
    });

    it('renders the table when list view is selected', async () => {
      localStorage.setItem(VM_LIST_VIEW_PREF_KEY, 'list');

      renderPage();

      expect(await screen.findByRole('columnheader', { name: 'Name' })).toBeInTheDocument();
      expect(screen.getByRole('link', { name: 'web-01' })).toBeInTheDocument();
    });

    it('switches from cards to the table via the view switcher', async () => {
      const user = userEvent.setup();

      renderPage();
      await screen.findByRole('link', { name: 'web-01' });
      expect(screen.queryByRole('columnheader', { name: 'Name' })).not.toBeInTheDocument();

      await user.click(screen.getByRole('button', { name: 'List view' }));

      expect(await screen.findByRole('columnheader', { name: 'Name' })).toBeInTheDocument();
    });
  });

  describe('filter data errors', () => {
    it('shows an alert and still renders VMs when instance types fail to load', async () => {
      const user = userEvent.setup();

      vi.mocked(useListResource).mockReturnValue(
        mockQueryResult({
          data: { items: [] },
          isLoading: false,
          error: new Error('Instance types unavailable'),
        }) as ReturnType<typeof useListResource>,
      );

      renderPage();

      await screen.findByRole('link', { name: 'web-01' });
      await user.click(screen.getByRole('button', { name: 'Filter by instance type' }));

      expect(await screen.findByText('Unable to load instance types')).toBeInTheDocument();
      expect(screen.getByText('Instance types unavailable')).toBeInTheDocument();
      expect(screen.getByRole('link', { name: 'Create virtual machine' })).toHaveAttribute(
        'href',
        '/vms/create',
      );
      expect(screen.getByText('Standard 4 vCPU / 8 GiB')).toBeInTheDocument();
    });
  });

  describe('VM list errors', () => {
    it('keeps compute instance failures on the page-level error path', async () => {
      mockVmList({
        vms: [],
        totalItems: 0,
        error: new Error('VMs unavailable'),
      });

      renderPage();

      expect(await screen.findByText('An error occurred')).toBeInTheDocument();
      expect(screen.getByText('VMs unavailable')).toBeInTheDocument();
      expect(screen.queryByRole('link', { name: 'web-01' })).not.toBeInTheDocument();
    });
  });
});
