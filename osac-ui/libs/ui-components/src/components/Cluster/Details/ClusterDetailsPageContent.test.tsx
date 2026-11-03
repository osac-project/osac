import { useState } from 'react';
import { create } from '@bufbuild/protobuf';
import { act, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  type Cluster,
  ClusterSchema,
  ClusterState,
  ClusterVersionSchema,
  ClusterVersionState,
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
    observedVersion: '4.17.0',
    apiEndpoint: 'api.example.com',
    ingressEndpoint: 'apps.example.com',
  },
});

const eligibleVersion = create(ClusterVersionSchema, {
  id: 'version-4-18',
  metadata: { name: 'ocp-4-18' },
  spec: { version: '4.18.0', state: ClusterVersionState.ACTIVE, enabled: true },
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
  it('shows the observed version rail on every tab with a safe unavailable reason', async () => {
    const { user } = renderWithProviders(<ClusterDetailsPageContent cluster={cluster} />, {
      apiFixtures: { externalIpAttachments: [] },
    });

    expect(screen.queryByText('Cluster version')).toBeInTheDocument();
    expect(screen.getByText('4.17.0')).toBeInTheDocument();
    const upgradeButton = screen.getByRole('button', { name: 'Upgrade cluster' });
    expect(upgradeButton).toBeDisabled();
    expect(upgradeButton).toHaveAccessibleDescription('Upgrade eligibility is unavailable.');

    await user.click(screen.getByRole('tab', { name: 'Networking' }));
    expect(screen.getByText('Cluster version')).toBeInTheDocument();
    await user.click(screen.getByRole('tab', { name: 'Node sets' }));
    expect(screen.getByText('Cluster version')).toBeInTheDocument();
    expect(screen.queryByRole('dialog', { name: 'Upgrade cluster' })).not.toBeInTheDocument();
  });

  it('opens one cluster upgrade dialog for eligible targets and returns focus on Cancel', async () => {
    const { user } = renderWithProviders(
      <ClusterDetailsPageContent
        cluster={cluster}
        upgrade={{ action: { kind: 'available' }, eligibleVersions: [eligibleVersion] }}
      />,
      { apiFixtures: { externalIpAttachments: [] } },
    );

    const upgradeButton = screen.getByRole('button', { name: 'Upgrade cluster' });
    expect(upgradeButton).toBeEnabled();
    await user.click(upgradeButton);

    const dialog = screen.getByRole('dialog', { name: 'Upgrade cluster' });
    expect(within(dialog).getByText('4.17.0')).toBeInTheDocument();
    expect(within(dialog).getByLabelText(/^Target version/)).toBeInTheDocument();
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }));

    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: 'Upgrade cluster' })).not.toBeInTheDocument(),
    );
    await waitFor(() => expect(upgradeButton).toHaveFocus());
  });

  it('closes and clears the upgrade dialog when the cluster identity changes', async () => {
    const otherCluster = create(ClusterSchema, {
      id: 'cluster-2',
      status: { state: ClusterState.READY, observedVersion: '4.17.1' },
    });
    const controller: { setCluster?: (next: Cluster) => void } = {};
    const Host = () => {
      const [currentCluster, setCurrentCluster] = useState(cluster);
      controller.setCluster = setCurrentCluster;
      return (
        <ClusterDetailsPageContent
          cluster={currentCluster}
          upgrade={{ action: { kind: 'available' }, eligibleVersions: [eligibleVersion] }}
        />
      );
    };
    const { user } = renderWithProviders(<Host />, {
      apiFixtures: { externalIpAttachments: [] },
    });

    await user.click(screen.getByRole('button', { name: 'Upgrade cluster' }));
    await user.click(screen.getByLabelText(/^Target version/));
    await user.click(screen.getByRole('option', { name: '4.18.0' }));
    expect(screen.getByRole('button', { name: /^Target version/ })).toHaveTextContent('4.18.0');

    act(() => controller.setCluster?.(otherCluster));
    expect(screen.getAllByText('4.17.1').length).toBeGreaterThan(0);
    expect(screen.queryByRole('dialog', { name: 'Upgrade cluster' })).not.toBeInTheDocument();

    act(() => controller.setCluster?.(cluster));
    expect(screen.queryByRole('dialog', { name: 'Upgrade cluster' })).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Upgrade cluster' }));
    expect(screen.getByRole('button', { name: /^Target version/ })).toHaveTextContent(
      'Select a target version',
    );
  });

  it('keeps a supplied blocked reason associated with the disabled upgrade action', () => {
    renderWithProviders(
      <ClusterDetailsPageContent
        cluster={cluster}
        upgrade={{
          action: { kind: 'blocked', reason: 'A cluster operation is in progress.' },
          eligibleVersions: [],
        }}
      />,
      { apiFixtures: { externalIpAttachments: [] } },
    );

    const upgradeButton = screen.getByRole('button', { name: 'Upgrade cluster' });
    expect(upgradeButton).toBeDisabled();
    expect(upgradeButton).toHaveAccessibleDescription('A cluster operation is in progress.');
    expect(screen.queryByRole('dialog', { name: 'Upgrade cluster' })).not.toBeInTheDocument();
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
