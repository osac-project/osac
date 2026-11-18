import { clone, create } from '@bufbuild/protobuf';
import { TimestampSchema } from '@bufbuild/protobuf/wkt';
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import {
  ClusterConditionType,
  ClusterSchema,
  ClusterState,
  ConditionStatus,
  ExternalIPAttachmentEndpoint,
  ExternalIPAttachmentSchema,
  ExternalIPAttachmentState,
  ExternalIPAttachments,
  ExternalIPAttachmentsCreateResponseSchema,
  ExternalIPAttachmentsDeleteResponseSchema,
  ExternalIPSchema,
  ExternalIPState,
} from '@osac/types';
import type { ExternalIPAttachmentsCreateRequest } from '@osac/types';

import ClusterDetailsPageContent from './ClusterDetailsPageContent';
import { createMockConnectTransport } from '../../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const cluster = create(ClusterSchema, {
  id: 'cluster-1',
  metadata: {
    name: 'production-cluster',
    project: 'project-a',
    creator: 'cluster-owner',
    creationTimestamp: create(TimestampSchema, { seconds: 1735689600n }),
  },
  spec: {
    catalogItem: { id: 'catalog-1', name: 'Standard' },
    nodeSets: {
      compute: { size: 3, baremetalInstanceType: { name: 'standard-host' } },
      gpu: { size: 2, baremetalInstanceType: { name: 'accelerated-host' } },
    },
    networkAttachment: {
      subnet: { name: 'tenant-subnet' },
      securityGroups: [{ id: 'sg-1', name: 'tenant-security' }],
    },
    network: { podCidr: '10.128.0.0/14', serviceCidr: '172.30.0.0/16' },
  },
  status: {
    state: ClusterState.READY,
    observedVersion: '4.17.0',
    apiUrl: 'https://api.example.com:6443',
    consoleUrl: 'https://console.example.com',
    apiEndpoint: 'api.example.com',
    ingressEndpoint: 'apps.example.com',
    nodeSets: { compute: { size: 99 } },
    conditions: [
      {
        type: ClusterConditionType.READY,
        status: ConditionStatus.TRUE,
        reason: 'Provisioned',
        message: 'All nodes are ready',
      },
    ],
  },
});

const externalIp = create(ExternalIPSchema, {
  id: 'eip-1',
  metadata: { name: 'edge-ip' },
  status: {
    state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED,
    attached: false,
    address: '203.0.113.10',
  },
});

const attachment = create(ExternalIPAttachmentSchema, {
  id: 'attachment-1',
  spec: {
    externalIp: { id: 'eip-1' },
    target: { case: 'cluster', value: { id: 'cluster-1' } },
    targetEndpoint: ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
  },
  status: {
    state: ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_READY,
    externalIpAddress: '203.0.113.10',
  },
});

const endpoints = [
  ['API', ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API],
  ['Ingress', ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS],
] as const;

describe('ClusterDetailsPageContent', () => {
  it('keeps compact details in the main column and both cards in the rail', async () => {
    const { container } = renderWithProviders(<ClusterDetailsPageContent cluster={cluster} />);

    expect(screen.getByRole('heading', { level: 1, name: 'production-cluster' })).toHaveClass(
      'pf-m-2xl',
    );
    expect(screen.getByRole('link', { name: 'Clusters' })).toHaveAttribute('href', '/clusters');
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1);
    const titleRow = screen.getByRole('heading', { level: 1 }).parentElement?.parentElement;
    const prefix = titleRow?.querySelector('.catalog-item-icon.m-is-active');
    expect(prefix).toHaveAttribute('aria-hidden', 'true');
    expect(prefix?.querySelector('svg')).not.toBeNull();
    expect(
      (prefix?.compareDocumentPosition(screen.getByRole('heading', { level: 1 })) ?? 0) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).not.toBe(0);
    expect(
      screen.getAllByRole('heading', { level: 2 }).map((heading) => heading.textContent),
    ).toEqual([
      'Overview',
      'Lifecycle',
      'Networking',
      'Conditions',
      'Cluster version',
      'External IP endpoints',
    ]);
    for (const heading of screen.getAllByRole('heading', { level: 2 })) {
      expect(heading.querySelector('svg')).toBeNull();
    }
    expect(screen.queryByRole('tablist')).not.toBeInTheDocument();
    const cards = container.querySelectorAll<HTMLElement>('.pf-v6-c-card');
    expect(cards).toHaveLength(2);
    for (const card of cards) {
      expect(card).toHaveClass('pf-m-secondary');
      const header = card.querySelector('.pf-v6-c-card__header');
      expect(header).not.toBeNull();
      expect(header?.nextElementSibling?.tagName).toBe('HR');
      expect(header?.nextElementSibling?.nextElementSibling).toHaveClass('pf-v6-c-card__body');
      expect(card.querySelectorAll(':scope > hr')).toHaveLength(1);
    }
    expect(cards[0]).toContainElement(screen.getByRole('heading', { name: 'Cluster version' }));
    expect(cards[0]).toContainElement(screen.getByRole('heading', { name: 'Node sets' }));
    expect(cards[0]).toContainElement(screen.getByRole('heading', { name: 'Settings' }));
    expect(cards[1]).toContainElement(
      screen.getByRole('heading', { name: 'External IP endpoints' }),
    );
    const grid = container.querySelector('.cluster-details__grid');
    const mainColumn = container.querySelector('.cluster-details__main');
    const rail = container.querySelector('.cluster-details__rail');
    expect(grid?.children).toHaveLength(2);
    expect(grid?.firstElementChild).toBe(mainColumn);
    expect(grid?.lastElementChild).toBe(rail);
    for (const name of ['Overview', 'Lifecycle', 'Networking', 'Conditions']) {
      expect(mainColumn).toContainElement(screen.getByRole('heading', { name }));
      expect(rail).not.toContainElement(screen.getByRole('heading', { name }));
    }
    expect(mainColumn?.querySelectorAll('hr')).toHaveLength(2);
    expect(rail?.querySelectorAll('hr')).toHaveLength(4);
    for (const card of cards) {
      expect(rail).toContainElement(card);
      expect(mainColumn).not.toContainElement(card);
    }
    const networkTable = screen.getByRole('grid', { name: 'Cluster network attachment' });
    const conditionsHeading = screen.getByRole('heading', { name: 'Conditions' });
    expect(mainColumn).toContainElement(networkTable);
    expect(
      networkTable.compareDocumentPosition(conditionsHeading) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).not.toBe(0);
    expect(
      conditionsHeading.compareDocumentPosition(cards[0]) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).not.toBe(0);
    const overview = screen.getByLabelText('Cluster overview');
    expect(overview.querySelector('dt')?.textContent).toBe('Status');
    expect(within(overview).getByText('Ready')).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 1 }).parentElement).not.toContainElement(
      within(overview).getByText('Ready'),
    );
    expect(screen.getAllByText('Ready')).toHaveLength(1);
    expect(screen.queryByRole('button', { name: 'Actions' })).not.toBeInTheDocument();
    for (const name of ['Download kubeconfig', 'View password', 'Delete']) {
      expect(screen.getByRole('button', { name })).toBeInTheDocument();
    }
    expect(screen.getByText('Standard')).toBeInTheDocument();
    expect(screen.queryByText('Worker nodes')).not.toBeInTheDocument();
    expect(screen.queryByText('5')).not.toBeInTheDocument();
    expect(screen.queryByText('99')).not.toBeInTheDocument();
    expect(screen.getByText('https://api.example.com:6443').closest('a')).toBeNull();
    expect(screen.getByText('https://console.example.com').closest('a')).toBeNull();
    expect(screen.queryByText('Creator')).not.toBeInTheDocument();
    expect(screen.queryByText('cluster-owner')).not.toBeInTheDocument();
    expect(screen.getByText('Created')).toBeInTheDocument();
    expect(container.querySelector('time')).toHaveAttribute('datetime', '2025-01-01T00:00:00.000Z');
    expect(screen.getByText('4.17.0')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Upgrade cluster' })).not.toBeInTheDocument();
    expect(screen.getByText('Upgrade eligibility is unavailable.')).toBeInTheDocument();
    const networking = screen.getByRole('grid', { name: 'Cluster network attachment' });
    expect(within(networking).getByRole('cell', { name: 'tenant-subnet' })).toBeInTheDocument();
    expect(within(networking).getByRole('cell', { name: 'tenant-security' })).toBeInTheDocument();
    expect(screen.getByText('10.128.0.0/14')).toBeInTheDocument();
    expect(screen.getByText('172.30.0.0/16')).toBeInTheDocument();
    const nodeSets = screen.getByRole('list', { name: 'Cluster node sets' });
    expect(
      within(nodeSets)
        .getAllByRole('listitem')
        .map((row) => row.textContent),
    ).toEqual(['computestandard-host-3 Nodes-—', 'gpuaccelerated-host-2 Nodes-—']);
    expect(nodeSets.closest('.pf-v6-l-grid__item')).toBeNull();
    expect(cards[0]).toContainElement(nodeSets);
    const conditions = screen.getByRole('grid', { name: 'Cluster conditions' });
    expect(
      within(conditions).getByRole('cell', { name: 'All nodes are ready' }),
    ).toBeInTheDocument();
    expect(conditions.closest('.pf-v6-l-grid__item')).toBeNull();
    expect(conditions.closest('.cluster-details__conditions')).toContainElement(conditionsHeading);
    expect(conditions.closest('.cluster-details__networking-table')).toBeNull();
    expect(networking.closest('.cluster-details__networking-table')).not.toBeNull();
    expect(
      within(conditions)
        .getAllByRole('columnheader')
        .map((header) => header.textContent),
    ).toEqual(['Type', 'Status', 'Reason', 'Message', 'Last transition']);
    await screen.findByText('api.example.com');
    expect(screen.getByText('apps.example.com')).toBeInTheDocument();
  });

  it('resets open history when the page changes resource identity', async () => {
    const { user, rerender } = renderWithProviders(<ClusterDetailsPageContent cluster={cluster} />);
    await user.click(screen.getByRole('button', { name: 'Upgrade history' }));
    expect(screen.getByRole('dialog', { name: 'Upgrade history' })).toBeInTheDocument();
    rerender(
      <ClusterDetailsPageContent
        cluster={create(ClusterSchema, { ...cluster, id: 'another-cluster' })}
      />,
    );
    expect(screen.queryByRole('dialog', { name: 'Upgrade history' })).not.toBeInTheDocument();
  });

  it('starts one real attachment list request on entry, without duplicating it when dialogs open and close', async () => {
    const transport = createMockConnectTransport({ externalIps: [externalIp] });
    const unary = vi.spyOn(transport, 'unary');
    const attachmentRequests = () =>
      unary.mock.calls.filter(([method]) => method === ExternalIPAttachments.method.list);
    const { user } = renderWithProviders(<ClusterDetailsPageContent cluster={cluster} />, {
      transport,
    });

    await screen.findByText('api.example.com');
    expect(attachmentRequests()).toHaveLength(1);
    expect(attachmentRequests()[0][4]).toMatchObject({
      filter: 'this.spec.cluster.id == "cluster-1"',
    });
    await user.click(screen.getAllByRole('button', { name: 'Attach External IP' })[0]);
    await screen.findByRole('heading', { name: 'Attach external IP to API endpoint' });
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(attachmentRequests()).toHaveLength(1);
  });

  it.each(endpoints)(
    'submits the exact %s endpoint attach payload from the continuous page',
    async (label, endpoint) => {
      const createAttachment = vi.fn((request: ExternalIPAttachmentsCreateRequest) =>
        create(ExternalIPAttachmentsCreateResponseSchema, {
          object: { id: 'new-attachment', spec: request.object?.spec },
        }),
      );
      const { user } = renderWithProviders(<ClusterDetailsPageContent cluster={cluster} />, {
        apiFixtures: { externalIps: [externalIp] },
        transportOverrides: { onExternalIpAttachmentCreate: createAttachment },
      });

      await screen.findByText('api.example.com');
      await user.click(
        screen.getAllByRole('button', { name: 'Attach External IP' })[label === 'API' ? 0 : 1],
      );
      expect(
        screen.getByRole('heading', { name: `Attach external IP to ${label} endpoint` }),
      ).toBeInTheDocument();
      const dialog = within(screen.getByRole('dialog'));
      await waitFor(() => expect(dialog.getByRole('combobox')).toHaveValue('edge-ip'));
      await user.click(dialog.getByRole('button', { name: 'Attach' }));
      await waitFor(() => expect(createAttachment).toHaveBeenCalledTimes(1));
      expect(createAttachment.mock.calls[0][0]).toMatchObject({
        object: {
          metadata: { project: 'project-a' },
          spec: {
            externalIp: { id: 'eip-1' },
            target: { case: 'cluster', value: { id: 'cluster-1' } },
            targetEndpoint: endpoint,
          },
        },
      });
      expect(createAttachment.mock.calls[0][0].object?.metadata?.name).toMatch(/^eipa-/);
      await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    },
  );

  it.each(endpoints)(
    'confirms and deletes the %s endpoint attachment by its existing ID',
    async (label, endpoint) => {
      const endpointAttachment = clone(ExternalIPAttachmentSchema, attachment);
      if (!endpointAttachment.spec) {
        throw new Error('The attachment fixture needs spec');
      }
      endpointAttachment.spec.targetEndpoint = endpoint;
      const deleteAttachment = vi.fn(() => create(ExternalIPAttachmentsDeleteResponseSchema));
      const { user } = renderWithProviders(<ClusterDetailsPageContent cluster={cluster} />, {
        apiFixtures: { externalIpAttachments: [endpointAttachment] },
        transportOverrides: { onExternalIpAttachmentDelete: deleteAttachment },
      });

      await user.click(
        await screen.findByRole('button', { name: `Detach External IP from ${label} endpoint` }),
      );
      expect(screen.getByRole('heading', { name: /Delete 203\.0\.113\.10\?/ })).toBeInTheDocument();
      expect(
        screen.getByText(`This detaches the external IP from the ${label} endpoint.`),
      ).toBeInTheDocument();
      expect(deleteAttachment).not.toHaveBeenCalled();
      await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete' }));
      await waitFor(() =>
        expect(deleteAttachment).toHaveBeenCalledExactlyOnceWith(
          expect.objectContaining({ id: 'attachment-1' }),
        ),
      );
      await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    },
  );

  it('closes an open attachment dialog when the resource identity changes', async () => {
    const { user, rerender } = renderWithProviders(<ClusterDetailsPageContent cluster={cluster} />);
    await screen.findByText('api.example.com');
    await user.click(screen.getAllByRole('button', { name: 'Attach External IP' })[0]);
    expect(screen.getByRole('dialog')).toBeInTheDocument();

    const anotherCluster = clone(ClusterSchema, cluster);
    anotherCluster.id = 'cluster-2';
    rerender(<ClusterDetailsPageContent cluster={anotherCluster} />);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await screen.findByText('api.example.com');
  });

  it('shows both node set and condition empty states without hiding other sections', async () => {
    renderWithProviders(
      <ClusterDetailsPageContent cluster={create(ClusterSchema, { id: 'empty' })} />,
    );
    expect(screen.getByText('No node sets configured.')).toBeInTheDocument();
    expect(screen.getByText('No conditions reported.')).toBeInTheDocument();
    expect(screen.queryByText('Worker nodes')).not.toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getAllByRole('button', { name: 'Attach External IP' })).toHaveLength(2),
    );
    for (const button of screen.getAllByRole('button', { name: 'Attach External IP' })) {
      expect(button).toBeDisabled();
    }
  });
});
