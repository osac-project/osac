import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
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

describe('ClusterDetailsPageContent', () => {
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
