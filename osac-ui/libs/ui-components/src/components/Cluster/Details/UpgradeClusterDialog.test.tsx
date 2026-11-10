import { create, toJson } from '@bufbuild/protobuf';
import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect';
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import {
  type Cluster,
  ClusterSchema,
  type ClusterVersion,
  ClusterVersionSchema,
  ClusterVersionState,
  Clusters,
  type ClustersUpdateRequest,
} from '@osac/types';

import UpgradeClusterDialog from './UpgradeClusterDialog';
import { useGetResource } from '../../../api/use-resource';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const activeVersion = create(ClusterVersionSchema, {
  id: 'version-4-18',
  metadata: { name: 'ocp-4-18' },
  spec: { version: '4.18.0', state: ClusterVersionState.ACTIVE, enabled: true },
});
const deprecatedVersion = create(ClusterVersionSchema, {
  id: 'version-4-19',
  metadata: { name: 'ocp-4-19' },
  spec: { version: '4.19.0', state: ClusterVersionState.DEPRECATED, enabled: true },
});

type UpdateResult = { object?: Cluster };

const renderDialog = ({
  eligibleVersions = [activeVersion, deprecatedVersion],
  currentVersion = '4.17.0',
  workerNodeCount = 2,
  onUpdate,
}: {
  eligibleVersions?: ClusterVersion[];
  currentVersion?: string;
  workerNodeCount?: number;
  onUpdate?: (request: ClustersUpdateRequest) => UpdateResult | Promise<UpdateResult>;
} = {}) => {
  const requests: ClustersUpdateRequest[] = [];
  const onClose = vi.fn();
  const transport = createRouterTransport((router) => {
    router.service(Clusters, {
      update: (request) => {
        requests.push(request);
        return onUpdate?.(request) ?? { object: request.object };
      },
    });
  });
  const view = renderWithProviders(
    <UpgradeClusterDialog
      clusterId="cluster-1"
      currentVersion={currentVersion}
      eligibleVersions={eligibleVersions}
      workerNodeCount={workerNodeCount}
      onClose={onClose}
    />,
    { transport },
  );
  return { ...view, onClose, requests };
};

const selectTarget = async (user: ReturnType<typeof renderDialog>['user'], name: string) => {
  await user.click(screen.getByLabelText(/^Target version/));
  await user.click(screen.getByRole('option', { name }));
};

const ClusterObservation = () => {
  const { data } = useGetResource(Clusters, { id: 'cluster-1' });
  return (
    <output aria-label="Refetched cluster version">
      {data?.object?.status?.observedVersion || '—'}
    </output>
  );
};

describe('UpgradeClusterDialog', () => {
  it('shows one required target choice, observed current version, guidance, and the external risk link', async () => {
    const { user, onClose, requests } = renderDialog();

    expect(screen.getByText('Current version')).toBeInTheDocument();
    expect(screen.getByText('4.17.0')).toBeInTheDocument();
    const targetToggle = screen.getByLabelText(/^Target version/);
    expect(targetToggle).toHaveAccessibleDescription('Choose a target version to continue.');
    expect(screen.getByRole('button', { name: /Upgrade cluster/ })).toBeDisabled();
    expect(screen.getByText(/control plane upgrades first/i)).toBeInTheDocument();
    expect(screen.getByText(/node sets automatically follow/i)).toBeInTheDocument();
    expect(screen.getByText(/cannot be cancelled or rolled back/i)).toBeInTheDocument();
    expect(screen.queryByText('Desired version')).not.toBeInTheDocument();
    expect(screen.queryByText('Node set version')).not.toBeInTheDocument();

    const link = screen.getByRole('link', { name: 'Check upgrade path and risks' });
    expect(link).toHaveAttribute(
      'href',
      'https://access.redhat.com/labs/ocpupgradegraph/update_path/',
    );
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noopener noreferrer');

    await user.click(targetToggle);
    const activeOption = screen.getByRole('option', { name: '4.18.0' });
    expect(activeOption).toBeInTheDocument();
    expect(screen.getByRole('dialog')).toContainElement(activeOption);
    expect(screen.getByRole('option', { name: '4.19.0 (deprecated)' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onClose).toHaveBeenCalledOnce();
    expect(requests).toHaveLength(0);
  });

  it('requires downtime acknowledgement before submitting for one worker node', async () => {
    const { user, requests } = renderDialog({ workerNodeCount: 1 });

    expect(
      screen.getByText('Workloads may be unavailable while the single worker node upgrades.'),
    ).toBeInTheDocument();
    const acknowledgement = screen.getByRole('checkbox', {
      name: 'I understand this may cause downtime',
    });
    expect(acknowledgement).not.toBeChecked();
    expect(acknowledgement).toBeRequired();

    await selectTarget(user, '4.18.0');
    const submitButton = screen.getByRole('button', { name: /Upgrade cluster/ });
    expect(submitButton).toBeDisabled();
    await user.click(acknowledgement);
    expect(acknowledgement).toBeChecked();
    expect(submitButton).toBeEnabled();

    await user.click(submitButton);
    await waitFor(() => expect(requests).toHaveLength(1));
  });

  it('does not require downtime acknowledgement with multiple worker nodes', async () => {
    const { user } = renderDialog({ workerNodeCount: 2 });

    expect(
      screen.queryByText('Workloads may be unavailable while the single worker node upgrades.'),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('checkbox', { name: 'I understand this may cause downtime' }),
    ).not.toBeInTheDocument();

    await selectTarget(user, '4.18.0');
    expect(screen.getByRole('button', { name: /Upgrade cluster/ })).toBeEnabled();
  });

  it('closes only the target menu on Escape and keeps the selected target', async () => {
    const { user, onClose, requests } = renderDialog();
    const targetToggle = screen.getByLabelText(/^Target version/);

    await selectTarget(user, '4.18.0');
    await user.click(targetToggle);
    await user.keyboard('{Escape}');

    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(targetToggle).toHaveTextContent('4.18.0');
    expect(targetToggle).toHaveAttribute('aria-expanded', 'false');
    expect(requests).toHaveLength(0);
  });

  it('lets keyboard users open the target menu and select a version', async () => {
    const { user, requests } = renderDialog();
    const dialog = screen.getByRole('dialog');
    const targetToggle = within(dialog).getByLabelText(/^Target version/);

    targetToggle.focus();
    await user.keyboard('{Enter}');
    const option = await screen.findByRole('option', { name: '4.18.0' });
    expect(dialog).toContainElement(option);
    await user.keyboard('{ArrowDown}{Enter}');

    expect(targetToggle).toHaveTextContent('4.18.0');
    expect(requests).toHaveLength(0);
  });

  it('submits one ID-backed Cluster update with exactly the spec.version mask', async () => {
    const { user, onClose, requests } = renderDialog();

    await selectTarget(user, '4.18.0');
    expect(screen.getByRole('button', { name: /Upgrade cluster/ })).toBeEnabled();
    await user.click(screen.getByRole('button', { name: /Upgrade cluster/ }));

    await waitFor(() => expect(requests).toHaveLength(1));
    expect(requests[0].object?.id).toBe('cluster-1');
    expect(requests[0].object?.spec?.version?.id).toBe('version-4-18');
    expect(requests[0].updateMask?.paths).toEqual(['spec.version']);
    expect(toJson(ClusterSchema, requests[0].object ?? create(ClusterSchema))).toEqual({
      id: 'cluster-1',
      spec: { version: { id: 'version-4-18' } },
    });
    await waitFor(() => expect(onClose).toHaveBeenCalledOnce());
  });

  it('refetches an active generic Cluster query after an accepted upgrade', async () => {
    let getCalls = 0;
    let updateCalls = 0;
    let observedVersion = '4.17.0';
    const onClose = vi.fn();
    const transport = createRouterTransport((router) => {
      router.service(Clusters, {
        get: () => {
          getCalls += 1;
          return {
            object: create(ClusterSchema, {
              id: 'cluster-1',
              status: { observedVersion },
            }),
          };
        },
        update: (request) => {
          updateCalls += 1;
          observedVersion = '4.18.0';
          return { object: request.object };
        },
      });
    });
    const { user } = renderWithProviders(
      <>
        <ClusterObservation />
        <UpgradeClusterDialog
          clusterId="cluster-1"
          currentVersion="4.17.0"
          eligibleVersions={[activeVersion]}
          workerNodeCount={2}
          onClose={onClose}
        />
      </>,
      { transport },
    );

    await waitFor(() =>
      expect(screen.getByLabelText('Refetched cluster version')).toHaveTextContent('4.17.0'),
    );
    expect(getCalls).toBe(1);
    await selectTarget(user, '4.18.0');
    await user.click(screen.getByRole('button', { name: /Upgrade cluster/ }));

    await waitFor(() =>
      expect(screen.getByLabelText('Refetched cluster version')).toHaveTextContent('4.18.0'),
    );
    expect(updateCalls).toBe(1);
    expect(getCalls).toBe(2);
    expect(onClose).toHaveBeenCalledOnce();
  });

  it('keeps an empty target list disabled', () => {
    renderDialog({ eligibleVersions: [] });

    expect(screen.getByText('No upgrade targets are available.')).toBeInTheDocument();
    expect(screen.getByLabelText(/^Target version/)).toBeDisabled();
    expect(screen.getByRole('button', { name: /Upgrade cluster/ })).toBeDisabled();
  });

  it('prevents another submission or cancellation while the request is pending', async () => {
    let finishUpdate: ((result: UpdateResult) => void) | undefined;
    const pending = new Promise<UpdateResult>((resolve) => {
      finishUpdate = resolve;
    });
    const { user, onClose, requests } = renderDialog({ onUpdate: () => pending });

    await selectTarget(user, '4.18.0');
    await user.click(screen.getByRole('button', { name: /Upgrade cluster/ }));
    await waitFor(() => expect(requests).toHaveLength(1));
    expect(screen.getByRole('button', { name: /Upgrade cluster/ })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled();
    await user.click(screen.getByRole('button', { name: /Upgrade cluster/ }));
    await user.keyboard('{Escape}');
    expect(requests).toHaveLength(1);
    expect(onClose).not.toHaveBeenCalled();

    finishUpdate?.({ object: create(ClusterSchema, { id: 'cluster-1' }) });
    await waitFor(() => expect(onClose).toHaveBeenCalledOnce());
  });

  it('retains the selected target on error and retries only after another submit', async () => {
    let attempts = 0;
    const { user, onClose, requests } = renderDialog({
      onUpdate: (request) => {
        attempts += 1;
        if (attempts === 1) {
          throw new ConnectError('upgrade admission denied', Code.FailedPrecondition);
        }
        return { object: request.object };
      },
    });

    await selectTarget(user, '4.19.0 (deprecated)');
    await user.click(screen.getByRole('button', { name: /Upgrade cluster/ }));

    const errorMessage = await screen.findByText('upgrade admission denied');
    expect(errorMessage.closest('[role="alert"]')).not.toBeNull();
    expect(screen.getByLabelText(/^Target version/)).toHaveTextContent('4.19.0 (deprecated)');
    expect(screen.getByRole('button', { name: /Upgrade cluster/ })).toBeEnabled();
    expect(requests).toHaveLength(1);
    expect(onClose).not.toHaveBeenCalled();

    await user.click(screen.getByRole('button', { name: /Upgrade cluster/ }));
    await waitFor(() => expect(onClose).toHaveBeenCalledOnce());
    expect(requests).toHaveLength(2);
  });

  it('shows authorization denial inline without closing the dialog', async () => {
    const { user, onClose, requests } = renderDialog({
      onUpdate: () => {
        throw new ConnectError('permission denied', Code.PermissionDenied);
      },
    });

    await selectTarget(user, '4.18.0');
    await user.click(screen.getByRole('button', { name: /Upgrade cluster/ }));

    expect(
      await screen.findByText('You are not authorized to access this resource.'),
    ).toBeInTheDocument();
    expect(requests).toHaveLength(1);
    expect(onClose).not.toHaveBeenCalled();
  });
});
