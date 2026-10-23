import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ClusterCatalogItemSchema } from '@osac/types';

import ClusterCreateWizard from './ClusterCreateWizard';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

const VERSION_ID = 'cluster-version-4-17';
const PULL_SECRET_ID = 'secret-pull-1';

// Every governed field is locked with a valid default value, so the only user
// input required to reach Review is the resource name on the General step —
// this exercises the full wizard shell (catalog selection, step navigation,
// submission, and error handling) without duplicating the per-field coverage
// already in the individual step tests.
const clusterCatalogItem = create(ClusterCatalogItemSchema, {
  id: 'catalog-cluster-1',
  metadata: { name: 'catalog-cluster-1' },
  title: 'Small CaaS cluster',
  description: 'A small OpenShift cluster',
  published: true,
  fields: {
    version: {
      behavior: { case: 'locked', value: { id: VERSION_ID, name: '4-17-0' } },
    },
    sshPublicKey: {
      behavior: {
        case: 'locked',
        value:
          'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBJACfzqANDyW1ygNn0FWP7YBZ6XLt+XPGpSw5PyknOW user@host',
      },
    },
    pullSecretSecret: {
      behavior: { case: 'locked', value: { id: PULL_SECRET_ID, name: 'my-pull-secret' } },
    },
    network: {
      podCidr: { behavior: { case: 'locked', value: '10.128.0.0/14' } },
      serviceCidr: { behavior: { case: 'locked', value: '172.30.0.0/16' } },
    },
    nodeSets: {
      behavior: {
        case: 'locked',
        value: {
          items: {
            worker: { size: 3, baremetalInstanceType: { id: 'bm-1', name: 'standard' } },
          },
        },
      },
    },
    autoExternalIpAttachment: {
      behavior: { case: 'locked', value: false },
    },
    networkAttachment: {
      behavior: { case: 'locked', value: {} },
    },
  },
});

const renderWizard = (onClosed = vi.fn()) =>
  renderWithProviders(<ClusterCreateWizard onClosed={onClosed} />, {
    apiFixtures: { clusterCatalogItems: [clusterCatalogItem] },
    session: { role: 'tenant-user' },
  });

// The catalog card's selectable radio input does not currently expose an
// accessible name (see CatalogItemCard's selectableActionAriaLabel), so it is
// located by the id PatternFly derives from the catalog item id instead.
const clickCatalogItemCard = async (user: ReturnType<typeof renderWizard>['user']) => {
  await screen.findByText('catalog-cluster-1');
  const radio = document.getElementById('selectedCatalogItem-catalog-cluster-1');
  if (!radio) {
    throw new Error('Catalog item radio input not found');
  }
  await user.click(radio);
};

const selectCatalogItemAndFillName = async (
  user: ReturnType<typeof renderWizard>['user'],
  name: string,
) => {
  await clickCatalogItemCard(user);
  await user.click(screen.getByRole('button', { name: 'Next' }));
  await user.type(await screen.findByRole('textbox', { name: 'Name' }), name);
};

const advanceThroughRemainingSteps = async (user: ReturnType<typeof renderWizard>['user']) => {
  // Configuration and Networking: every governed field is locked with a
  // catalog default, so Next can be clicked without further input.
  await user.click(screen.getByRole('button', { name: 'Next' }));
  await user.click(screen.getByRole('button', { name: 'Next' }));
  await user.click(screen.getByRole('button', { name: 'Next' }));
};

describe('ClusterCreateWizard', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
  });

  it('submits the catalog-governed payload and navigates to the new cluster on success', async () => {
    const onClusterCreate = vi.fn((req: { object?: { metadata?: unknown; spec?: unknown } }) => ({
      object: { id: 'cluster-new-1', metadata: req.object?.metadata, spec: req.object?.spec },
    }));
    const { user } = renderWithProviders(<ClusterCreateWizard onClosed={vi.fn()} />, {
      apiFixtures: { clusterCatalogItems: [clusterCatalogItem] },
      transportOverrides: { onClusterCreate: onClusterCreate as never },
      session: { role: 'tenant-user' },
    });

    await selectCatalogItemAndFillName(user, 'my-cluster');
    await advanceThroughRemainingSteps(user);

    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => {
      expect(onClusterCreate).toHaveBeenCalledTimes(1);
    });
    const request = onClusterCreate.mock.calls[0][0];
    expect(request.object?.metadata).toMatchObject({ name: 'my-cluster' });
    expect(request.object?.spec).toMatchObject({
      catalogItem: { id: 'catalog-cluster-1' },
    });
    // Locked fields are governed entirely by the catalog item and must not be
    // re-sent by the client. `nodeSets` is a protobuf map field, so the wire
    // representation is an empty object rather than an absent property.
    expect(request.object?.spec).not.toHaveProperty('version');
    expect(request.object?.spec).not.toHaveProperty('pullSecretSecret');
    expect(
      Object.keys((request.object?.spec as { nodeSets?: object })?.nodeSets ?? {}),
    ).toHaveLength(0);

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith('/clusters/cluster-new-1');
    });
  });

  it('shows a submission error on the review step without navigating away', async () => {
    const { user } = renderWithProviders(<ClusterCreateWizard onClosed={vi.fn()} />, {
      apiFixtures: { clusterCatalogItems: [clusterCatalogItem] },
      transportOverrides: {
        onClusterCreate: () => {
          throw new ConnectError('Cluster quota exceeded', Code.ResourceExhausted);
        },
      },
      session: { role: 'tenant-user' },
    });

    await selectCatalogItemAndFillName(user, 'my-cluster');
    await advanceThroughRemainingSteps(user);
    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => {
      expect(screen.getByText('Cluster quota exceeded')).toBeInTheDocument();
    });
    expect(mockNavigate).not.toHaveBeenCalled();
  });

  it('calls onClosed when Cancel is clicked', async () => {
    const onClosed = vi.fn();
    const { user } = renderWizard(onClosed);

    await user.click(await screen.findByRole('button', { name: 'Cancel' }));

    expect(onClosed).toHaveBeenCalledTimes(1);
  });

  it('blocks Next on the catalog step until a catalog item is selected', async () => {
    const { user } = renderWizard();

    await screen.findByText('catalog-cluster-1');
    await user.click(screen.getByRole('button', { name: 'Next' }));

    expect(screen.getByText('Fix the highlighted errors before continuing.')).toBeInTheDocument();
  });
});
