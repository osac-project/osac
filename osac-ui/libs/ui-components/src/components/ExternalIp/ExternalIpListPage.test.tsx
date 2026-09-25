import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
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
import { SessionProvider } from '../../hooks/use-session';
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
): ExternalIP =>
  create(ExternalIPSchema, {
    id,
    metadata: { name, project: 'default' },
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
) =>
  renderWithProviders(
    <SessionProvider role="tenant-user" username="test-user" tenantId="test-tenant">
      <ExternalIpListPage />
    </SessionProvider>,
    {
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
    },
  );

describe('ExternalIpListPage', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
  });

  it('renders the page header', () => {
    renderPage();

    expect(screen.queryByText('Networking')).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'External IPs' })).toBeInTheDocument();
    expect(
      screen.getByText(
        'Review provider-assigned IP pools and allocate external addresses for edge exposure.',
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
      'Project',
      'Address',
      'IP pool',
      'Created',
      '',
    ]);
    expect(screen.queryByRole('link', { name: 'edge-ip' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: '203.0.113.10' })).not.toBeInTheDocument();
    expect(screen.getByText('Allocated')).toBeInTheDocument();
    expect(screen.getByText('In use')).toBeInTheDocument();
    expect(screen.getByText('Virtual machine')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Virtual machine: web-1' })).toHaveAttribute(
      'href',
      '/vms/vm-1',
    );
    expect(screen.queryByRole('columnheader', { name: 'Attached' })).not.toBeInTheDocument();
    expect(screen.queryByText('No')).not.toBeInTheDocument();
    expect(screen.getAllByText('public-edge').length).toBeGreaterThan(0);
    expect(screen.getAllByText('12 available').length).toBe(2);
  });

  it('shows empty state when there are no external IPs', async () => {
    renderPage([]);

    await waitFor(() => {
      expect(
        screen.getByText('No external IPs yet. Create one to get started.'),
      ).toBeInTheDocument();
    });
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it('shows a filtered empty state when a project filter has no results', async () => {
    renderPage([], defaultAttachments, [], ['/networking/external-ips?project=default']);

    await waitFor(() => {
      expect(screen.getByText('No external IPs match your search.')).toBeInTheDocument();
    });
    expect(
      screen.queryByText('No external IPs yet. Create one to get started.'),
    ).not.toBeInTheDocument();
  });

  it('links Create external IP to the create route', () => {
    renderPage();

    expect(screen.getByRole('link', { name: 'Create external IP' })).toHaveAttribute(
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

  it('links a NAT-consumed IP to its virtual network', async () => {
    renderPage(
      [makeExternalIp('eip-2', 'nat-ip', '203.0.113.11', 'pool-1', true)],
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
      expect(screen.getByText('NAT gateway')).toBeInTheDocument();
      expect(screen.getByRole('link', { name: 'NAT gateway: edge-nat' })).toHaveAttribute(
        'href',
        '/networking/virtual-networks/vn-1',
      );
    });
  });

  it('disables delete when the external IP is attached', async () => {
    const { user } = renderPage();

    await waitFor(() => {
      expect(screen.getByText('203.0.113.11')).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Actions for nat-ip' }));

    expect(screen.getByRole('menuitem', { name: /Delete/ })).toBeDisabled();
  });
});
