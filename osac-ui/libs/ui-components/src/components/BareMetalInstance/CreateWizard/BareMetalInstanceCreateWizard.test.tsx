import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { BareMetalInstanceCatalogItemSchema, BareMetalInstanceRunStrategy } from '@osac/types';

import BareMetalInstanceCreateWizard from './BareMetalInstanceCreateWizard';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

const INSTANCE_TYPE_ID = 'bm-type-standard';
const DISK_IMAGE_ID = 'disk-image-rhel-9';

// Every governed field is locked with a valid default value, so the only user
// input required to reach Review is the resource name on the General step —
// this exercises the full wizard shell (catalog selection, step navigation,
// submission, and error handling) without duplicating the per-field coverage
// already in the individual step tests. `userData` is locked with non-empty
// content so the Review step's authentication-method check is satisfied.
const bareMetalCatalogItem = create(BareMetalInstanceCatalogItemSchema, {
  id: 'catalog-bm-1',
  metadata: { name: 'catalog-bm-1' },
  title: 'Small bare metal host',
  description: 'A small bare metal instance',
  published: true,
  fields: {
    runStrategy: { behavior: { case: 'locked', value: BareMetalInstanceRunStrategy.HALTED } },
    userData: { behavior: { case: 'locked', value: '#cloud-config\nruncmd: []' } },
    instanceType: {
      behavior: { case: 'locked', value: { id: INSTANCE_TYPE_ID, name: 'standard' } },
    },
    diskImage: {
      behavior: { case: 'locked', value: { id: DISK_IMAGE_ID, name: 'rhel-9' } },
    },
  },
});

const renderWizard = (onClosed = vi.fn()) =>
  renderWithProviders(<BareMetalInstanceCreateWizard onClosed={onClosed} />, {
    apiFixtures: { bareMetalCatalogItems: [bareMetalCatalogItem] },
    session: { role: 'tenant-user' },
  });

// The catalog card's selectable radio input does not currently expose an
// accessible name (see CatalogItemCard's selectableActionAriaLabel), so it is
// located by the id PatternFly derives from the catalog item id instead.
const clickCatalogItemCard = async (user: ReturnType<typeof renderWizard>['user']) => {
  await screen.findByText('catalog-bm-1');
  const radio = document.getElementById('selectedCatalogItem-catalog-bm-1');
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

describe('BareMetalInstanceCreateWizard', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
  });

  it('submits the catalog-governed payload and navigates to the new instance on success', async () => {
    const onBareMetalInstanceCreate = vi.fn(
      (req: { object?: { metadata?: unknown; spec?: unknown } }) => ({
        object: { id: 'bm-new-1', metadata: req.object?.metadata, spec: req.object?.spec },
      }),
    );
    const { user } = renderWithProviders(<BareMetalInstanceCreateWizard onClosed={vi.fn()} />, {
      apiFixtures: { bareMetalCatalogItems: [bareMetalCatalogItem] },
      transportOverrides: { onBareMetalInstanceCreate: onBareMetalInstanceCreate as never },
      session: { role: 'tenant-user' },
    });

    await selectCatalogItemAndFillName(user, 'my-bmi');
    await advanceThroughRemainingSteps(user);

    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => {
      expect(onBareMetalInstanceCreate).toHaveBeenCalledTimes(1);
    });
    const request = onBareMetalInstanceCreate.mock.calls[0][0];
    expect(request.object?.metadata).toMatchObject({ name: 'my-bmi' });
    expect(request.object?.spec).toMatchObject({
      catalogItem: { id: 'catalog-bm-1' },
    });
    // Locked fields are governed entirely by the catalog item and must not be
    // re-sent by the client.
    expect(request.object?.spec).not.toHaveProperty('instanceType');
    expect(request.object?.spec).not.toHaveProperty('diskImage');
    expect(request.object?.spec).not.toHaveProperty('userData');
    expect(request.object?.spec).not.toHaveProperty('runStrategy');

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith('/bare-metal/bm-new-1');
    });
  });

  it('shows a submission error on the review step without navigating away', async () => {
    const { user } = renderWithProviders(<BareMetalInstanceCreateWizard onClosed={vi.fn()} />, {
      apiFixtures: { bareMetalCatalogItems: [bareMetalCatalogItem] },
      transportOverrides: {
        onBareMetalInstanceCreate: () => {
          throw new ConnectError('Host type is at capacity', Code.ResourceExhausted);
        },
      },
      session: { role: 'tenant-user' },
    });

    await selectCatalogItemAndFillName(user, 'my-bmi');
    await advanceThroughRemainingSteps(user);
    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => {
      expect(screen.getByText('Host type is at capacity')).toBeInTheDocument();
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

    await screen.findByText('catalog-bm-1');
    await user.click(screen.getByRole('button', { name: 'Next' }));

    expect(screen.getByText('Fix the highlighted errors before continuing.')).toBeInTheDocument();
  });
});
