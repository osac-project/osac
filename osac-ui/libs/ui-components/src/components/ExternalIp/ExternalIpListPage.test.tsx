import { create } from '@bufbuild/protobuf';
import { screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type {
  ExternalIP,
  ExternalIPAttachment,
  ExternalIPPool,
  NATGateway,
  Project,
} from '@osac/types';
import {
  ExternalIPAttachmentSchema,
  ExternalIPPoolSchema,
  ExternalIPSchema,
  ExternalIPState,
  IPFamily,
  NATGatewaySchema,
  ProjectState,
} from '@osac/types';

import ExternalIpListPage from './ExternalIpListPage';
import { renderWithProviders } from '../../test-utils/TestProviders';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

const makePool = (id: string, name: string): ExternalIPPool =>
  create(ExternalIPPoolSchema, {
    id,
    metadata: { name },
    spec: { ipFamily: IPFamily.IP_FAMILY_IPV4 },
    status: { available: 12n },
  });

const makeExternalIp = (
  id: string,
  name: string,
  address: string,
  poolId: string,
  attached = false,
  labels: Record<string, string> = {},
): ExternalIP =>
  create(ExternalIPSchema, {
    id,
    metadata: { name, project: 'default', labels },
    spec: { pool: { id: poolId, name: 'public-edge' } },
    status: {
      state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED,
      address,
      pool: poolId,
      attached,
    },
  });

const defaultIps = [
  makeExternalIp('eip-1', 'edge-ip', '203.0.113.10', 'pool-1'),
  makeExternalIp('eip-2', 'nat-ip', '203.0.113.11', 'pool-1', true),
];

const defaultAttachments: ExternalIPAttachment[] = [
  create(ExternalIPAttachmentSchema, {
    id: 'att-1',
    spec: {
      externalIp: { id: 'eip-2' },
      target: { case: 'computeInstance', value: { id: 'vm-1', name: 'web-1' } },
    },
  }),
];

const renderPage = (
  externalIps: ExternalIP[] = defaultIps,
  attachments: ExternalIPAttachment[] = defaultAttachments,
  natGateways: NATGateway[] = [],
  routerEntries?: string[],
  role: 'tenant-user' | 'admin' = 'tenant-user',
) =>
  renderWithProviders(<ExternalIpListPage />, {
    apiFixtures: {
      externalIps,
      externalIpPools: [makePool('pool-1', 'public-edge')],
      externalIpAttachments: attachments,
      natGateways,
      projects: [
        {
          id: 'p-1',
          metadata: { name: 'default' },
          spec: { title: 'Default' },
          status: { state: ProjectState.ACTIVE },
        } as Project,
      ],
    },
    routerEntries,
    session: { role },
  });

describe('ExternalIpListPage', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
  });

  it('renders the page header', async () => {
    renderPage();

    expect(screen.queryByText('Networking')).not.toBeInTheDocument();
    expect(await screen.findByRole('heading', { name: 'External IPs' })).toBeInTheDocument();
    expect(
      screen.getByText(
        'Addresses from shared provider pools that make workloads reachable from outside the virtual network.',
      ),
    ).toBeInTheDocument();
  });

  it('renders a row per external IP', async () => {
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('203.0.113.10')).toBeInTheDocument();
    });
    expect(screen.getByText('edge-ip')).toBeInTheDocument();
    expect(screen.getByText('nat-ip')).toBeInTheDocument();
    expect(screen.getByText('203.0.113.11')).toBeInTheDocument();
    expect(screen.getAllByRole('columnheader').map((header) => header.textContent)).toEqual([
      'Name',
      'Status',
      'Address',
      'IP pool',
      'Used by',
      'Project',
      '',
    ]);
    expect(screen.queryByRole('link', { name: 'edge-ip' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: '203.0.113.10' })).not.toBeInTheDocument();
    // The status is the API state; Used by carries the usage.
    expect(screen.getAllByText('Allocated')).toHaveLength(2);
    expect(screen.queryByText('In use')).not.toBeInTheDocument();
    expect(screen.getByText('(Virtual machine)')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Virtual machine: web-1' })).toHaveAttribute(
      'href',
      '/vms/vm-1',
    );
    expect(screen.queryByRole('columnheader', { name: 'Attached' })).not.toBeInTheDocument();
    expect(screen.queryByText('No')).not.toBeInTheDocument();
    expect(screen.getAllByText('public-edge').length).toBeGreaterThan(0);
    // The pool's capacity is on the pools page; tenants have no pool details page to open.
    expect(screen.queryByText('12 available')).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'public-edge' })).not.toBeInTheDocument();
    // The project is named as on the Projects page and links to its details.
    const projectLinks = await screen.findAllByRole('link', { name: 'Default' });
    expect(projectLinks).toHaveLength(2);
    expect(projectLinks[0]).toHaveAttribute('href', '/projects/p-1');
  });

  it('links the IP pool to its details page for a provider admin', async () => {
    renderPage(defaultIps, defaultAttachments, [], undefined, 'admin');

    const poolLinks = await screen.findAllByRole('link', { name: 'public-edge' });
    expect(poolLinks[0]).toHaveAttribute('href', '/admin/infrastructure/external-ip-pools/pool-1');
  });

  it('names the root project Root, as the rest of the console does', async () => {
    renderPage([
      create(ExternalIPSchema, {
        id: 'eip-root',
        metadata: { name: 'root-ip', project: '' },
        status: { state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED, address: '203.0.113.30' },
      }),
    ]);

    const row = (await screen.findByText('root-ip')).closest('tr') as HTMLElement;
    expect(within(row).getByText('Root')).toBeInTheDocument();
  });

  it('says where an address is reachable without promising the Internet', async () => {
    const { user } = renderPage();

    const address = await screen.findByRole('columnheader', { name: /Address/ });
    await user.hover(within(address).getByRole('button'));

    expect(await screen.findByText('Reachable where your provider routes it.')).toBeInTheDocument();
    expect(screen.queryByText(/internet/i)).not.toBeInTheDocument();
  });

  it('shows the resolved pool name when the reference has only an ID', async () => {
    const autoCreated = create(ExternalIPSchema, {
      id: 'eip-auto',
      metadata: { name: 'auto-ip', project: 'default' },
      spec: { pool: { id: 'pool-1' } },
      status: {
        state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED,
        address: '203.0.113.12',
        pool: 'pool-1',
        attached: true,
      },
    });

    renderPage([autoCreated], [], []);

    await waitFor(() => {
      expect(screen.getByText('203.0.113.12')).toBeInTheDocument();
    });
    expect(screen.getByText('public-edge')).toBeInTheDocument();
  });

  it('shows empty state when there are no external IPs', async () => {
    renderPage([]);

    expect(await screen.findByRole('heading', { name: 'No external IPs' })).toBeInTheDocument();
    expect(screen.getAllByRole('link', { name: 'Create external IP' })).toHaveLength(2);
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it('shows a filtered empty state when a project filter has no results', async () => {
    renderPage([], defaultAttachments, [], ['/networking/external-ips?project=default']);

    expect(await screen.findByRole('heading', { name: 'No results found' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Clear all filters' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'No external IPs' })).not.toBeInTheDocument();
  });

  it('links Create external IP to the create route', async () => {
    renderPage();

    expect(await screen.findByRole('link', { name: 'Create external IP' })).toHaveAttribute(
      'href',
      '/networking/external-ips/create',
    );
  });

  it('opens the delete confirmation dialog from the kebab', async () => {
    const { user } = renderPage();

    await waitFor(() => {
      expect(screen.getByText('203.0.113.10')).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Actions for edge-ip' }));
    await user.click(screen.getByRole('menuitem', { name: 'Delete' }));

    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(screen.getByText('Delete edge-ip?')).toBeInTheDocument();
  });

  it('shows a NAT gateway consumer under Used by although status.attached is false', async () => {
    renderPage(
      // The server never sets status.attached for a NATGateway consumer.
      [makeExternalIp('eip-2', 'nat-ip', '203.0.113.11', 'pool-1', false)],
      [],
      [
        create(NATGatewaySchema, {
          id: 'nat-1',
          metadata: { name: 'edge-nat' },
          spec: {
            virtualNetwork: { id: 'vn-1', name: 'edge-net' },
            externalIp: { id: 'eip-2' },
          },
        }),
      ],
    );

    await waitFor(() => {
      expect(screen.getByText('(NAT gateway)')).toBeInTheDocument();
      expect(screen.getByRole('link', { name: 'NAT gateway: edge-nat' })).toHaveAttribute(
        'href',
        '/networking/virtual-networks/vn-1?tab=nat-gateway',
      );
    });
    expect(screen.getByText('Allocated')).toBeInTheDocument();
  });

  it('disables delete with a reason when a NAT gateway uses the external IP', async () => {
    const { user } = renderPage(
      [makeExternalIp('eip-2', 'nat-ip', '203.0.113.11', 'pool-1', false)],
      [],
      [
        create(NATGatewaySchema, {
          id: 'nat-1',
          metadata: { name: 'edge-nat' },
          spec: { virtualNetwork: { id: 'vn-1' }, externalIp: { id: 'eip-2' } },
        }),
      ],
    );

    await screen.findByRole('link', { name: 'NAT gateway: edge-nat' });
    await user.click(screen.getByRole('button', { name: 'Actions for nat-ip' }));

    const deleteItem = screen.getByRole('menuitem', { name: 'Delete' });
    expect(deleteItem).toHaveAttribute('aria-disabled', 'true');
    await user.hover(deleteItem);
    expect(await screen.findByText('Delete NAT gateway edge-nat first.')).toBeInTheDocument();
    await user.click(deleteItem);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('shows Default and Auto badges and disables Delete on those external IPs', async () => {
    const { user } = renderPage(
      [
        makeExternalIp('eip-d', 'default-nat-ip', '203.0.113.20', 'pool-1', false, {
          'osac.openshift.io/default': 'true',
        }),
        makeExternalIp('eip-a', 'auto-eip-c1-api', '203.0.113.21', 'pool-1', false, {
          'osac.openshift.io/auto-created': 'true',
          'osac.openshift.io/auto-created-for': 'c1',
          'osac.openshift.io/auto-created-kind': 'cluster',
        }),
      ],
      [],
    );

    expect(await screen.findByText('Default')).toBeInTheDocument();
    const auto = screen.getByText('Auto');
    await user.hover(auto);
    expect(
      await screen.findByText('Created automatically for cluster c1. It is deleted with it.'),
    ).toBeInTheDocument();
    await user.unhover(auto);

    await user.click(screen.getByRole('button', { name: 'Actions for default-nat-ip' }));
    const deleteDefault = screen.getByRole('menuitem', { name: 'Delete' });
    expect(deleteDefault).toHaveAttribute('aria-disabled', 'true');
    await user.hover(deleteDefault);
    expect(
      await screen.findByText('Created as a tenant default. It is deleted with the tenant.'),
    ).toBeInTheDocument();
    await user.keyboard('{Escape}');

    await user.click(screen.getByRole('button', { name: 'Actions for auto-eip-c1-api' }));
    expect(screen.getByRole('menuitem', { name: 'Delete' })).toHaveAttribute(
      'aria-disabled',
      'true',
    );
  });

  it('disables delete when the external IP is attached', async () => {
    const { user } = renderPage();

    await waitFor(() => {
      expect(screen.getByText('203.0.113.11')).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Actions for nat-ip' }));

    const deleteItem = screen.getByRole('menuitem', { name: /Delete/ });
    expect(deleteItem).toHaveAttribute('aria-disabled', 'true');
    expect(deleteItem).not.toBeDisabled();

    await user.click(deleteItem);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
});
