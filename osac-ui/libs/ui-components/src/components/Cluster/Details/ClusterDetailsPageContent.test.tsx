import { create } from '@bufbuild/protobuf';
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  ClusterConditionType,
  ClusterSchema,
  ClusterState,
  ExternalIPAttachmentEndpoint,
  ExternalIPAttachmentState,
  ExternalIPState,
} from '@osac/types';
import type { ExternalIP, ExternalIPAttachment } from '@osac/types';

import ClusterDetailsPageContent from './ClusterDetailsPageContent';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const cluster = create(ClusterSchema, {
  id: 'cluster-1',
  metadata: { project: 'project-a' },
  status: {
    state: ClusterState.READY,
    apiEndpoint: 'api.example.com',
    ingressEndpoint: 'apps.example.com',
  },
});

const externalIp = {
  id: 'eip-1',
  metadata: { name: 'edge-ip' },
  status: {
    state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED,
    attached: false,
    address: '203.0.113.10',
  },
} as ExternalIP;

const attachment = {
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
} as unknown as ExternalIPAttachment;

const makeProgressingCluster = (reason?: string) =>
  create(ClusterSchema, {
    id: 'cluster-1',
    metadata: { name: 'cluster-one', project: 'project-a' },
    status: {
      state: ClusterState.PROGRESSING,
      apiUrl: 'https://api.example.com:6443',
      consoleUrl: 'https://console.example.com',
      conditions: [
        {
          type: ClusterConditionType.PROGRESSING,
          reason,
          message: 'Waiting for cluster workers',
        },
      ],
    },
  });

describe('ClusterDetailsPageContent', () => {
  it('shows provisioning activity in the header while preserving conditions and endpoint URLs', () => {
    renderWithProviders(
      <ClusterDetailsPageContent cluster={makeProgressingCluster('WorkersJoining')} />,
    );

    const titleRow = screen.getByRole('heading', { name: 'cluster-one' }).closest('.pf-v6-l-flex');
    expect(titleRow?.querySelector('.pf-v6-c-label')).toHaveTextContent('Provisioning');
    expect(titleRow?.querySelector('small')).toHaveTextContent('WorkersJoining');
    expect(titleRow?.querySelectorAll('.pf-v6-c-label')).toHaveLength(1);

    const conditions = screen.getByRole('grid', { name: 'Cluster conditions' });
    expect(within(conditions).getByRole('columnheader', { name: 'Reason' })).toBeInTheDocument();
    expect(within(conditions).getByRole('columnheader', { name: 'Message' })).toBeInTheDocument();
    expect(within(conditions).getByText('WorkersJoining')).toBeInTheDocument();
    expect(within(conditions).getByText('Waiting for cluster workers')).toBeInTheDocument();

    const summary = screen.getByRole('group', { name: 'Cluster summary' });
    expect(within(summary).getByText('https://api.example.com:6443')).toBeInTheDocument();
    expect(within(summary).getByText('https://console.example.com')).toBeInTheDocument();
  });

  it('shows Stalled as a warning in the header', () => {
    renderWithProviders(<ClusterDetailsPageContent cluster={makeProgressingCluster('Stalled')} />);

    const warning = screen.getByLabelText('Warning: Provisioning stalled');
    expect(warning).toHaveTextContent('Provisioning');
    expect(warning).toHaveClass('pf-m-orange');
    const titleRow = screen.getByRole('heading', { name: 'cluster-one' }).closest('.pf-v6-l-flex');
    expect(titleRow?.querySelector('small')).toHaveTextContent('Stalled');
    expect(titleRow?.querySelectorAll('.pf-v6-c-label')).toHaveLength(1);
  });

  it.each(['StageUnknown', undefined])(
    'shows Stage unknown in the header for reason %s',
    (reason) => {
      renderWithProviders(<ClusterDetailsPageContent cluster={makeProgressingCluster(reason)} />);

      const titleRow = screen
        .getByRole('heading', { name: 'cluster-one' })
        .closest('.pf-v6-l-flex');
      expect(titleRow?.querySelector('.pf-v6-c-label')).toHaveTextContent('Provisioning');
      expect(titleRow?.querySelector('small')).toHaveTextContent('Stage unknown');
    },
  );

  it('replaces the prior header activity when the public cluster changes', () => {
    const { rerender } = renderWithProviders(
      <ClusterDetailsPageContent cluster={makeProgressingCluster('AllocatingWorkers')} />,
    );
    const titleRow = () =>
      screen.getByRole('heading', { name: 'cluster-one' }).closest('.pf-v6-l-flex');
    expect(titleRow()?.querySelector('small')).toHaveTextContent('AllocatingWorkers');

    rerender(<ClusterDetailsPageContent cluster={makeProgressingCluster('WorkersJoining')} />);
    expect(titleRow()?.querySelector('small')).toHaveTextContent('WorkersJoining');

    rerender(
      <ClusterDetailsPageContent
        cluster={create(ClusterSchema, {
          id: 'cluster-1',
          metadata: { name: 'cluster-one' },
          status: { state: ClusterState.READY },
        })}
      />,
    );
    expect(titleRow()?.querySelector('small')).not.toBeInTheDocument();
    expect(titleRow()?.querySelector('.pf-v6-c-label')).toHaveTextContent('Ready');
  });

  it('shows conditions only on Overview and External IP endpoints in the Networking side card', async () => {
    const { user } = renderWithProviders(<ClusterDetailsPageContent cluster={cluster} />, {
      apiFixtures: { externalIpAttachments: [] },
    });

    expect(screen.getByText('Conditions')).toBeInTheDocument();
    expect(screen.queryByText('External IP endpoints')).not.toBeInTheDocument();

    await user.click(screen.getByRole('tab', { name: 'Networking' }));

    expect(screen.queryByText('Conditions')).not.toBeInTheDocument();
    expect(screen.getByText('External IP endpoints')).toBeInTheDocument();

    await user.click(screen.getByRole('tab', { name: 'Node sets' }));

    expect(screen.queryByText('Conditions')).not.toBeInTheDocument();
    expect(screen.queryByText('External IP endpoints')).not.toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Node sets' })).toHaveAttribute('aria-selected', 'true');
  });

  it('opens the endpoint attach modal from the Networking side card', async () => {
    const { user } = renderWithProviders(<ClusterDetailsPageContent cluster={cluster} />, {
      apiFixtures: { externalIps: [externalIp], externalIpAttachments: [] },
    });

    await user.click(screen.getByRole('tab', { name: 'Networking' }));
    await waitFor(() => {
      expect(screen.getAllByRole('button', { name: 'Attach External IP' })).toHaveLength(2);
    });
    await user.click(screen.getAllByRole('button', { name: 'Attach External IP' })[0]);

    expect(
      screen.getByRole('heading', { name: 'Attach external IP to API endpoint' }),
    ).toBeInTheDocument();
  });

  it('opens the endpoint detach confirmation modal from the Networking side card', async () => {
    const { user } = renderWithProviders(<ClusterDetailsPageContent cluster={cluster} />, {
      apiFixtures: { externalIpAttachments: [attachment] },
    });

    await user.click(screen.getByRole('tab', { name: 'Networking' }));
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: 'Detach External IP from API endpoint' }),
      ).toBeInTheDocument();
    });
    await user.click(screen.getByRole('button', { name: 'Detach External IP from API endpoint' }));

    expect(screen.getByRole('heading', { name: /Delete 203\.0\.113\.10\?/ })).toBeInTheDocument();
  });
});
