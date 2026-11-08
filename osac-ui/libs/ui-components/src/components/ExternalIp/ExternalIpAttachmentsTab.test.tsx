import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type {
  BareMetalInstance,
  Cluster,
  ComputeInstance,
  ComputeInstancesListRequest,
  ExternalIP,
  ExternalIPAttachment,
  ExternalIPAttachmentsCreateRequest,
} from '@osac/types';
import {
  ClusterState,
  ComputeInstanceState,
  ExternalIPAttachmentEndpoint,
  ExternalIPAttachmentSchema,
  ExternalIPAttachmentState,
  ExternalIPAttachmentsCreateResponseSchema,
  ExternalIPSchema,
  ExternalIPState,
  ServiceTier,
} from '@osac/types';
import type { Tenant } from '@osac/types/private';

import ExternalIpListPage from './ExternalIpListPage';
import type { UserRole } from '../../shellTypes';
import type { MockTransportOverrides } from '../../test-utils/createMockConnectTransport';
import { getResourceSelectInput } from '../../test-utils/query';
import { renderWithProviders } from '../../test-utils/TestProviders';

const vmAttachment = create(ExternalIPAttachmentSchema, {
  id: 'att-vm',
  metadata: { name: 'eipa-web', tenant: 'tenant-a' },
  spec: {
    externalIp: { id: 'eip-used' },
    target: { case: 'computeInstance', value: { id: 'vm-1', name: 'web-1' } },
  },
  status: {
    state: ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_FAILED,
    externalIpAddress: '203.0.113.20',
    message: 'target endpoint not ready',
  },
});

const clusterAttachment = create(ExternalIPAttachmentSchema, {
  id: 'att-cluster',
  metadata: {
    name: 'auto-api',
    tenant: 'tenant-b',
    labels: {
      'osac.openshift.io/auto-created': 'true',
      'osac.openshift.io/auto-created-kind': 'cluster',
      'osac.openshift.io/auto-created-for': 'c1',
    },
  },
  spec: {
    externalIp: { id: 'eip-auto' },
    target: { case: 'cluster', value: { id: 'c1', name: 'prod' } },
    targetEndpoint: ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
  },
  status: {
    state: ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_READY,
    externalIpAddress: '203.0.113.21',
  },
});

const freeIp: ExternalIP = create(ExternalIPSchema, {
  id: 'eip-free',
  metadata: { name: 'eip-free', project: 'proj-a', tenant: 'tenant-a' },
  status: {
    state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED,
    attached: false,
    address: '203.0.113.30',
  },
});

const runningVm = {
  id: 'vm-2',
  metadata: { name: 'api-vm', project: 'proj-a', tenant: 'tenant-a' },
  status: { state: ComputeInstanceState.RUNNING },
} as ComputeInstance;
const stoppedVm = {
  id: 'vm-3',
  metadata: { name: 'stopped-vm', project: 'proj-a', tenant: 'tenant-a' },
  status: { state: ComputeInstanceState.STOPPED },
} as ComputeInstance;
const otherProjectVm = {
  id: 'vm-4',
  metadata: { name: 'other-vm', project: 'proj-b', tenant: 'tenant-a' },
  status: { state: ComputeInstanceState.RUNNING },
} as ComputeInstance;
// Same project name as the external IP, but another tenant: only a provider admin lists it.
const foreignTenantVm = {
  id: 'vm-5',
  metadata: { name: 'foreign-vm', project: 'proj-a', tenant: 'tenant-b' },
  status: { state: ComputeInstanceState.RUNNING },
} as ComputeInstance;
const otherProjectIp: ExternalIP = create(ExternalIPSchema, {
  id: 'eip-other',
  metadata: { name: 'eip-other', project: 'proj-b', tenant: 'tenant-a' },
  status: {
    state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED,
    attached: false,
    address: '203.0.113.31',
  },
});
const readyCluster = {
  id: 'c2',
  metadata: { name: 'dev-cluster', project: 'proj-a', tenant: 'tenant-a' },
  status: { state: ClusterState.READY },
} as Cluster;

const renderTab = ({
  role = 'tenant-user',
  attachments = [vmAttachment, clusterAttachment],
  externalIps = [freeIp, otherProjectIp],
  computeInstances = [runningVm, stoppedVm, otherProjectVm, foreignTenantVm],
  enabledServices,
  transportOverrides,
}: {
  role?: UserRole;
  attachments?: ExternalIPAttachment[];
  externalIps?: ExternalIP[];
  computeInstances?: ComputeInstance[];
  enabledServices?: ServiceTier[];
  transportOverrides?: MockTransportOverrides;
} = {}) =>
  renderWithProviders(<ExternalIpListPage />, {
    routerEntries: ['/networking/external-ips?tab=attachments'],
    apiFixtures: {
      ...(enabledServices ? { enabledServices } : {}),
      externalIps,
      externalIpAttachments: attachments,
      computeInstances,
      bareMetalInstances: [] as BareMetalInstance[],
      clusters: [readyCluster],
      tenants: [
        { id: 'tenant-a', metadata: { name: 'Acme' } },
        { id: 'tenant-b', metadata: { name: 'Globex' } },
      ] as Tenant[],
    },
    transportOverrides,
    session: { role },
  });

const rowOf = async (name: string) =>
  within((await screen.findByText(name)).closest('tr') as HTMLElement);

describe('External IPs Attachments tab', () => {
  it('opens from the URL and lists the attachments', async () => {
    renderTab();

    expect(screen.getByRole('tab', { name: 'Attachments' })).toHaveAttribute(
      'aria-selected',
      'true',
    );
    const vmRow = await rowOf('eipa-web');
    expect(screen.getAllByRole('columnheader').map((header) => header.textContent)).toEqual([
      'Name',
      'External IP',
      'Target',
      'Status',
      'Project',
      '',
    ]);
    expect(vmRow.getByText('203.0.113.20')).toBeInTheDocument();
    expect(vmRow.getByRole('link', { name: 'Virtual machine: web-1' })).toHaveAttribute(
      'href',
      '/vms/vm-1',
    );
    const clusterRow = await rowOf('auto-api');
    expect(clusterRow.getByRole('link', { name: 'Cluster: prod' })).toBeInTheDocument();
    expect(clusterRow.getByText('API endpoint')).toBeInTheDocument();
    expect(clusterRow.getByText('Auto')).toBeInTheDocument();
  });

  it('shows the status message and disables Delete on an automatic attachment', async () => {
    const { user } = renderTab();

    const vmRow = await rowOf('eipa-web');
    await user.click(vmRow.getByRole('button', { name: 'Failed' }));
    expect(await screen.findByText('target endpoint not ready')).toBeInTheDocument();

    const clusterRow = await rowOf('auto-api');
    await user.click(clusterRow.getByRole('button', { name: /Actions for/ }));
    const deleteItem = screen.getByRole('menuitem', { name: 'Delete' });
    expect(deleteItem).toHaveAttribute('aria-disabled', 'true');
    await user.hover(deleteItem);
    expect(await screen.findByText(/^Created automatically for cluster/)).toBeInTheDocument();
  });

  it('deletes an attachment and keeps the external IP', async () => {
    const onExternalIpAttachmentDelete = vi.fn().mockReturnValue({});
    const { user } = renderTab({ transportOverrides: { onExternalIpAttachmentDelete } });

    await user.click(await screen.findByRole('button', { name: 'Actions for eipa-web' }));
    await user.click(screen.getByRole('menuitem', { name: 'Delete' }));
    const dialog = within(screen.getByRole('dialog'));
    expect(dialog.getByText(/The external IP stays allocated/)).toBeInTheDocument();
    await user.click(dialog.getByRole('button', { name: 'Delete' }));

    await waitFor(() =>
      expect(onExternalIpAttachmentDelete).toHaveBeenCalledWith(
        expect.objectContaining({ id: 'att-vm' }),
      ),
    );
  });

  it('shows the empty state with Create attachment', async () => {
    renderTab({ attachments: [] });

    expect(await screen.findByRole('heading', { name: 'No attachments' })).toBeInTheDocument();
    expect(
      screen.getByText('Create an attachment to route an external IP to a workload.'),
    ).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Create attachment' })).toHaveLength(2);
  });

  it('shows the Tenant column and filter for a provider admin', async () => {
    const { user } = renderTab({ role: 'admin' });

    expect(await screen.findByRole('columnheader', { name: 'Tenant' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Filter by tenant' }));
    await user.click(screen.getByRole('option', { name: 'Globex' }));

    expect(screen.getByText('auto-api')).toBeInTheDocument();
    expect(screen.queryByText('eipa-web')).not.toBeInTheDocument();
  });

  it('creates an attachment to a ready virtual machine in the external IP project', async () => {
    let createRequest: ExternalIPAttachmentsCreateRequest | undefined;
    const { user } = renderTab({
      transportOverrides: {
        onExternalIpAttachmentCreate: (req) => {
          createRequest = req;
          return create(ExternalIPAttachmentsCreateResponseSchema, {
            object: { id: 'att-new' },
          });
        },
      },
    });

    await user.click(await screen.findByRole('button', { name: 'Create attachment' }));
    const dialog = within(await screen.findByRole('dialog'));
    expect(dialog.getByRole('radio', { name: 'Virtual machine' })).toBeChecked();
    expect(
      dialog.getByText("Only ready targets in the external IP's project are listed."),
    ).toBeInTheDocument();

    const externalIp = await waitFor(() => {
      const input = getResourceSelectInput(/^External IP$/);
      expect(input).toBeEnabled();
      return input;
    });
    await user.click(externalIp);
    await user.click(await screen.findByRole('option', { name: 'eip-free · 203.0.113.30' }));

    const target = await waitFor(() => {
      const input = getResourceSelectInput(/^Target$/);
      expect(input).toBeEnabled();
      return input;
    });
    await user.click(target);
    expect(await screen.findByRole('option', { name: 'api-vm' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'stopped-vm' })).not.toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'other-vm' })).not.toBeInTheDocument();
    await user.click(screen.getByRole('option', { name: 'api-vm' }));

    await user.click(dialog.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(createRequest).toBeDefined());
    expect(createRequest?.object?.spec?.externalIp?.id).toBe('eip-free');
    expect(createRequest?.object?.spec?.target).toEqual(
      expect.objectContaining({ case: 'computeInstance' }),
    );
    expect(createRequest?.object?.spec?.target.value?.id).toBe('vm-2');
    expect(createRequest?.object?.metadata?.project).toBe('proj-a');
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  });

  it('asks for the endpoint of a Cluster target and shows the server error', async () => {
    // After a radio click, jsdom reports the open typeahead menu as hidden; query it with hidden.
    let createRequest: ExternalIPAttachmentsCreateRequest | undefined;
    const { user } = renderTab({
      role: 'admin',
      transportOverrides: {
        onExternalIpAttachmentCreate: (req) => {
          createRequest = req;
          throw new ConnectError('cluster ingress endpoint is not ready', Code.FailedPrecondition);
        },
      },
    });

    await user.click(await screen.findByRole('button', { name: 'Create attachment' }));
    const dialog = within(await screen.findByRole('dialog'));
    await user.click(dialog.getByRole('radio', { name: 'Cluster' }));
    const externalIp = await waitFor(() => {
      const input = getResourceSelectInput(/^External IP$/);
      expect(input).toBeEnabled();
      return input;
    });
    await user.click(externalIp);
    await user.click(
      await screen.findByRole('option', { name: 'eip-free · 203.0.113.30', hidden: true }),
    );
    const target = await waitFor(() => {
      const input = getResourceSelectInput(/^Target$/);
      expect(input).toBeEnabled();
      return input;
    });
    await user.click(target);
    await user.click(await screen.findByRole('option', { name: 'dev-cluster', hidden: true }));
    await user.click(dialog.getByRole('radio', { name: 'Ingress' }));
    await user.click(dialog.getByRole('button', { name: 'Create' }));

    expect(await dialog.findByText('Failed to create attachment')).toBeInTheDocument();
    expect(dialog.getByText(/cluster ingress endpoint is not ready/)).toBeInTheDocument();
    expect(createRequest?.object?.spec?.targetEndpoint).toBe(
      ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
    );
    expect(createRequest?.object?.metadata?.tenant).toBe('tenant-a');
  });

  it('offers a provider admin only targets in the external IP tenant', async () => {
    const onComputeInstanceList = vi.fn();
    const { user } = renderTab({ role: 'admin', transportOverrides: { onComputeInstanceList } });

    await user.click(await screen.findByRole('button', { name: 'Create attachment' }));
    await screen.findByRole('dialog');
    const externalIp = await waitFor(() => {
      const input = getResourceSelectInput(/^External IP$/);
      expect(input).toBeEnabled();
      return input;
    });
    await user.click(externalIp);
    await user.click(await screen.findByRole('option', { name: 'eip-free · 203.0.113.30' }));
    const target = await waitFor(() => {
      const input = getResourceSelectInput(/^Target$/);
      expect(input).toBeEnabled();
      return input;
    });
    await user.click(target);

    expect(await screen.findByRole('option', { name: 'api-vm' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'foreign-vm' })).not.toBeInTheDocument();
    // The server filters too, so a full first page of other tenants cannot hide a target.
    const filters = onComputeInstanceList.mock.calls.map(
      ([req]) => (req as ComputeInstancesListRequest).filter,
    );
    expect(filters).toContainEqual(
      expect.stringMatching(
        /this\.metadata\.project == "proj-a".*this\.metadata\.tenant == "tenant-a"/,
      ),
    );
  });

  it('clears the target when the external IP changes', async () => {
    const onExternalIpAttachmentCreate = vi.fn();
    const { user } = renderTab({ transportOverrides: { onExternalIpAttachmentCreate } });

    await user.click(await screen.findByRole('button', { name: 'Create attachment' }));
    const dialog = within(await screen.findByRole('dialog'));
    const externalIp = await waitFor(() => {
      const input = getResourceSelectInput(/^External IP$/);
      expect(input).toBeEnabled();
      return input;
    });
    await user.click(externalIp);
    await user.click(await screen.findByRole('option', { name: 'eip-free · 203.0.113.30' }));
    const target = await waitFor(() => {
      const input = getResourceSelectInput(/^Target$/);
      expect(input).toBeEnabled();
      return input;
    });
    await user.click(target);
    await user.click(await screen.findByRole('option', { name: 'api-vm' }));

    await user.click(getResourceSelectInput(/^External IP$/));
    await user.click(await screen.findByRole('option', { name: 'eip-other · 203.0.113.31' }));
    await user.click(dialog.getByRole('button', { name: 'Create' }));

    expect(await dialog.findByText('A target is required')).toBeInTheDocument();
    expect(onExternalIpAttachmentCreate).not.toHaveBeenCalled();
  });

  it('disables the target again when the external IP is cleared', async () => {
    const { user } = renderTab();

    await user.click(await screen.findByRole('button', { name: 'Create attachment' }));
    const externalIp = await waitFor(() => {
      const input = getResourceSelectInput(/^External IP$/);
      expect(input).toBeEnabled();
      return input;
    });
    await user.click(externalIp);
    await user.click(await screen.findByRole('option', { name: 'eip-free · 203.0.113.30' }));
    await waitFor(() => expect(getResourceSelectInput(/^Target$/)).toBeEnabled());

    await user.click(screen.getByRole('button', { name: 'Clear input value' }));

    await waitFor(() =>
      expect(getResourceSelectInput(/^Target$/)).toHaveAttribute(
        'placeholder',
        'Select an external IP first',
      ),
    );
  });

  it('offers only root-project targets for an external IP in the root project', async () => {
    const rootIp: ExternalIP = create(ExternalIPSchema, {
      id: 'eip-root',
      metadata: { name: 'eip-root', project: '', tenant: 'tenant-a' },
      status: {
        state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED,
        attached: false,
        address: '203.0.113.32',
      },
    });
    const rootVm = {
      id: 'vm-root',
      metadata: { name: 'root-vm', project: '', tenant: 'tenant-a' },
      status: { state: ComputeInstanceState.RUNNING },
    } as ComputeInstance;
    const { user } = renderTab({
      externalIps: [rootIp],
      computeInstances: [rootVm, runningVm],
    });

    await user.click(await screen.findByRole('button', { name: 'Create attachment' }));
    const externalIp = await waitFor(() => {
      const input = getResourceSelectInput(/^External IP$/);
      expect(input).toBeEnabled();
      return input;
    });
    await user.click(externalIp);
    await user.click(await screen.findByRole('option', { name: 'eip-root · 203.0.113.32' }));
    const target = await waitFor(() => {
      const input = getResourceSelectInput(/^Target$/);
      expect(input).toBeEnabled();
      return input;
    });
    await user.click(target);

    expect(await screen.findByRole('option', { name: 'root-vm' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'api-vm' })).not.toBeInTheDocument();
  });

  it('offers an unused external IP that is past the first page', async () => {
    // Automatic addresses pass the server filter but are never offered.
    const automaticIps = Array.from({ length: 120 }, (_, index) =>
      create(ExternalIPSchema, {
        id: `eip-auto-${index}`,
        metadata: {
          name: `eip-auto-${index}`,
          project: 'proj-a',
          tenant: 'tenant-a',
          labels: { 'osac.openshift.io/auto-created': 'true' },
        },
        status: { state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED, attached: false },
      }),
    );
    const { user } = renderTab({ externalIps: [...automaticIps, freeIp] });

    await user.click(await screen.findByRole('button', { name: 'Create attachment' }));
    await screen.findByRole('dialog');
    const externalIp = await waitFor(() => {
      const input = getResourceSelectInput(/^External IP$/);
      expect(input).toBeEnabled();
      return input;
    });
    await user.click(externalIp);

    expect(
      await screen.findByRole('option', { name: 'eip-free · 203.0.113.30' }),
    ).toBeInTheDocument();
  });

  it('offers a ready target that is past the first page', async () => {
    const stoppedVms = Array.from(
      { length: 120 },
      (_, index) =>
        ({
          ...stoppedVm,
          id: `vm-stopped-${index}`,
          metadata: { ...stoppedVm.metadata, name: `stopped-${index}` },
        }) as ComputeInstance,
    );
    const { user } = renderTab({ computeInstances: [...stoppedVms, runningVm] });

    await user.click(await screen.findByRole('button', { name: 'Create attachment' }));
    await screen.findByRole('dialog');
    const externalIp = await waitFor(() => {
      const input = getResourceSelectInput(/^External IP$/);
      expect(input).toBeEnabled();
      return input;
    });
    await user.click(externalIp);
    await user.click(await screen.findByRole('option', { name: 'eip-free · 203.0.113.30' }));
    const target = await waitFor(() => {
      const input = getResourceSelectInput(/^Target$/);
      expect(input).toBeEnabled();
      return input;
    });
    await user.click(target);

    expect(await screen.findByRole('option', { name: 'api-vm' })).toBeInTheDocument();
  });

  it('loads no targets until an external IP is chosen', async () => {
    const onComputeInstanceList = vi.fn();
    const { user } = renderTab({ transportOverrides: { onComputeInstanceList } });

    await user.click(await screen.findByRole('button', { name: 'Create attachment' }));
    const dialog = within(await screen.findByRole('dialog'));
    await waitFor(() => expect(getResourceSelectInput(/^External IP$/)).toBeEnabled());

    expect(onComputeInstanceList).not.toHaveBeenCalled();
    expect(dialog.queryByText('No ready targets in this project')).not.toBeInTheDocument();
  });

  it('clears an earlier create error when the external IP changes', async () => {
    const { user } = renderTab({
      transportOverrides: {
        onExternalIpAttachmentCreate: () => {
          throw new ConnectError('target endpoint is not ready', Code.FailedPrecondition);
        },
      },
    });

    await user.click(await screen.findByRole('button', { name: 'Create attachment' }));
    const dialog = within(await screen.findByRole('dialog'));
    const externalIp = await waitFor(() => {
      const input = getResourceSelectInput(/^External IP$/);
      expect(input).toBeEnabled();
      return input;
    });
    await user.click(externalIp);
    await user.click(await screen.findByRole('option', { name: 'eip-free · 203.0.113.30' }));
    const target = await waitFor(() => {
      const input = getResourceSelectInput(/^Target$/);
      expect(input).toBeEnabled();
      return input;
    });
    await user.click(target);
    await user.click(await screen.findByRole('option', { name: 'api-vm' }));
    await user.click(dialog.getByRole('button', { name: 'Create' }));
    expect(await dialog.findByText('Failed to create attachment')).toBeInTheDocument();

    await user.click(getResourceSelectInput(/^External IP$/));
    await user.click(await screen.findByRole('option', { name: 'eip-other · 203.0.113.31' }));

    await waitFor(() =>
      expect(dialog.queryByText('Failed to create attachment')).not.toBeInTheDocument(),
    );
  });

  it('offers only the target types of enabled services', async () => {
    const { user } = renderTab({ enabledServices: [ServiceTier.VMAAS] });

    await user.click(await screen.findByRole('button', { name: 'Create attachment' }));
    const dialog = within(await screen.findByRole('dialog'));

    expect(dialog.getByRole('radio', { name: 'Virtual machine' })).toBeChecked();
    expect(dialog.queryByRole('radio', { name: 'Cluster' })).not.toBeInTheDocument();
    expect(dialog.queryByRole('radio', { name: 'Bare-metal instance' })).not.toBeInTheDocument();
  });

  it('requires an external IP and a target', async () => {
    const onExternalIpAttachmentCreate = vi.fn();
    const { user } = renderTab({ transportOverrides: { onExternalIpAttachmentCreate } });

    await user.click(await screen.findByRole('button', { name: 'Create attachment' }));
    const dialog = within(await screen.findByRole('dialog'));
    await user.click(dialog.getByRole('button', { name: 'Create' }));

    expect(await dialog.findByText('An external IP is required')).toBeInTheDocument();
    expect(dialog.getByText('A target is required')).toBeInTheDocument();
    expect(onExternalIpAttachmentCreate).not.toHaveBeenCalled();
  });
});

describe('External IPs Addresses tab', () => {
  it('is the default tab with Create external IP', async () => {
    const { user } = renderWithProviders(<ExternalIpListPage />, {
      routerEntries: ['/networking/external-ips'],
      apiFixtures: { externalIps: [freeIp] },
      session: { role: 'tenant-user' },
    });

    expect(screen.getByRole('tab', { name: 'Addresses' })).toHaveAttribute('aria-selected', 'true');
    expect(await screen.findByText('203.0.113.30')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Create external IP' })).toBeInTheDocument();

    await user.click(screen.getByRole('tab', { name: 'Attachments' }));
    expect(
      (await screen.findAllByRole('button', { name: 'Create attachment' })).length,
    ).toBeGreaterThan(0);
  });

  it('shows the Tenant column and filters by tenant for a provider admin', async () => {
    const otherTenantIp = create(ExternalIPSchema, {
      id: 'eip-b',
      metadata: { name: 'eip-b', tenant: 'tenant-b' },
      status: { state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED, address: '198.51.100.5' },
    });
    const { user } = renderWithProviders(<ExternalIpListPage />, {
      routerEntries: ['/networking/external-ips'],
      apiFixtures: {
        externalIps: [freeIp, otherTenantIp],
        tenants: [
          { id: 'tenant-a', metadata: { name: 'Acme' } },
          { id: 'tenant-b', metadata: { name: 'Globex' } },
        ] as Tenant[],
      },
      session: { role: 'admin' },
    });

    expect(await screen.findByRole('columnheader', { name: 'Tenant' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Filter by tenant' }));
    await user.click(screen.getByRole('option', { name: 'Globex' }));

    expect(await screen.findByText('198.51.100.5')).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByText('203.0.113.30')).not.toBeInTheDocument());
  });
});
