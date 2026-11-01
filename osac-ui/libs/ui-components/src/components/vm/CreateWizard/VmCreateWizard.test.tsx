import { clone, create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  ComputeInstanceCatalogItemFieldsSchema,
  ComputeInstanceCatalogItemSchema,
  ComputeInstanceRunStrategy,
  SubnetSchema,
  SubnetState,
} from '@osac/types';

import VmCreateWizard from './VmCreateWizard';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

const INSTANCE_TYPE_ID = 'it-standard-4-8';
const DISK_IMAGE_ID = 'disk-image-rhel-9';
const STORAGE_TIER_ID = 'tier-fast';

// Every governed field is locked with a valid default value, so the only user
// input required to reach Review is the resource name on the General step —
// this exercises the full wizard shell (catalog selection, step navigation,
// submission, and error handling) without duplicating the per-field coverage
// already in the individual step tests.
const vmCatalogItem = create(ComputeInstanceCatalogItemSchema, {
  id: 'catalog-vm-1',
  metadata: { name: 'catalog-vm-1' },
  title: 'RHEL 9 small VM',
  description: 'A small RHEL 9 compute instance',
  published: true,
  fields: {
    runStrategy: {
      behavior: {
        case: 'locked',
        value: ComputeInstanceRunStrategy.COMPUTE_INSTANCE_RUN_STRATEGY_HALTED,
      },
    },
    instanceType: {
      behavior: { case: 'locked', value: { id: INSTANCE_TYPE_ID, name: 'standard-4-8' } },
    },
    diskImage: {
      behavior: { case: 'locked', value: { id: DISK_IMAGE_ID, name: 'rhel-9' } },
    },
    bootDisk: {
      sizeGib: { behavior: { case: 'locked', value: 20 } },
      storageTier: {
        behavior: { case: 'locked', value: { id: STORAGE_TIER_ID, name: 'fast' } },
      },
    },
  },
});

const renderWizard = (onClosed = vi.fn()) =>
  renderWithProviders(<VmCreateWizard onClosed={onClosed} />, {
    apiFixtures: { catalogItems: [vmCatalogItem] },
    session: { role: 'tenant-user' },
  });

const clickCatalogItemCard = async (user: ReturnType<typeof renderWizard>['user']) => {
  const radio = await waitFor(() => {
    const option = document.getElementById('selectedCatalogItem-catalog-vm-1');
    if (!option) {
      throw new Error('Catalog item radio input not found');
    }
    return option;
  });
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
  // Configuration through Access: governed fields are locked with catalog defaults,
  // so Next can be clicked without further input.
  for (let step = 0; step < 5; step += 1) {
    await user.click(screen.getByRole('button', { name: 'Next' }));
  }
  await waitFor(() => {
    expect(screen.getByRole('button', { name: 'Create' })).toBeInTheDocument();
  });
};

describe('VmCreateWizard', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
  });

  it.each([false, true])(
    'rechecks selected subnet on Review before Create, locked=%s',
    async (locked) => {
      const catalog = clone(ComputeInstanceCatalogItemSchema, vmCatalogItem);
      const attachment = {
        items: [
          {
            virtualNetwork: { id: 'vn', name: 'network' },
            subnet: { id: 'subnet', name: 'subnet' },
          },
        ],
      };
      const networkFields = create(ComputeInstanceCatalogItemFieldsSchema, {
        networkAttachments: {
          behavior: locked
            ? { case: 'locked', value: attachment }
            : { case: 'editable', value: { defaultValue: attachment } },
        },
      });
      if (catalog.fields) {
        catalog.fields.networkAttachments = networkFields.networkAttachments;
      }
      let supported = true;
      const onCreate = vi.fn(() => ({ object: { id: 'new-vm' } }));
      const { user } = renderWithProviders(<VmCreateWizard onClosed={vi.fn()} />, {
        apiFixtures: { catalogItems: [catalog] },
        transportOverrides: {
          onSubnetGet: () => ({
            object: create(SubnetSchema, {
              id: 'subnet',
              spec: { virtualNetwork: { id: 'vn' } },
              status: { state: SubnetState.READY, computeInstanceSupported: supported },
            }),
          }),
          onComputeInstanceCreate: onCreate as never,
        },
      });
      await selectCatalogItemAndFillName(user, 'fresh-subnet-vm');
      await advanceThroughRemainingSteps(user);
      supported = false;
      await user.click(screen.getByRole('button', { name: 'Create' }));
      await screen.findByText('Fix the highlighted errors before continuing.');
      expect(onCreate).not.toHaveBeenCalled();
      expect(mockNavigate).not.toHaveBeenCalled();
    },
  );

  it('submits the catalog-governed payload and navigates to the new VM on success', async () => {
    const onComputeInstanceCreate = vi.fn(
      (req: { object?: { metadata?: unknown; spec?: unknown } }) => ({
        object: { id: 'vm-new-1', metadata: req.object?.metadata, spec: req.object?.spec },
      }),
    );
    const { user } = renderWithProviders(<VmCreateWizard onClosed={vi.fn()} />, {
      apiFixtures: { catalogItems: [vmCatalogItem] },
      transportOverrides: { onComputeInstanceCreate: onComputeInstanceCreate as never },
      session: { role: 'tenant-user' },
    });

    await selectCatalogItemAndFillName(user, 'my-vm');
    await advanceThroughRemainingSteps(user);

    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => {
      expect(onComputeInstanceCreate).toHaveBeenCalledTimes(1);
    });
    const request = onComputeInstanceCreate.mock.calls[0][0];
    expect(request.object?.metadata).toMatchObject({ name: 'my-vm' });
    expect(request.object?.spec).toMatchObject({
      catalogItem: { id: 'catalog-vm-1' },
    });
    // Locked fields are governed entirely by the catalog item and must not be
    // re-sent by the client.
    expect(request.object?.spec).not.toHaveProperty('instanceType');
    expect(request.object?.spec).not.toHaveProperty('diskImage');
    expect(request.object?.spec).not.toHaveProperty('bootDisk');
    expect(request.object?.spec).not.toHaveProperty('runStrategy');

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith('/vms/vm-new-1');
    });
  });

  it('shows a submission error on the review step without navigating away', async () => {
    const { user } = renderWithProviders(<VmCreateWizard onClosed={vi.fn()} />, {
      apiFixtures: { catalogItems: [vmCatalogItem] },
      transportOverrides: {
        onComputeInstanceCreate: () => {
          throw new ConnectError('Instance type is at capacity', Code.ResourceExhausted);
        },
      },
      session: { role: 'tenant-user' },
    });

    await selectCatalogItemAndFillName(user, 'my-vm');
    await advanceThroughRemainingSteps(user);
    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => {
      expect(screen.getByText('Instance type is at capacity')).toBeInTheDocument();
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

    await waitFor(() =>
      expect(document.getElementById('selectedCatalogItem-catalog-vm-1')).not.toBeNull(),
    );
    await user.click(screen.getByRole('button', { name: 'Next' }));

    expect(screen.getByText('Fix the highlighted errors before continuing.')).toBeInTheDocument();
  });
});
