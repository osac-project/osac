import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { StorageProtocol, StorageTierSchema, StorageTierState } from '@osac/types';

import { type ResourceSelectValue, emptyResourceSelectValue } from './resourceSelectValue';
import { StorageTierSelectField } from './StorageTierSelectField';
import { renderWithProviders } from '../../test-utils/TestProviders';

const makeTier = (
  name: string,
  displayName: string,
  description: string,
  state: StorageTierState = StorageTierState.ACTIVE,
  protocol: StorageProtocol = StorageProtocol.NFS,
) =>
  create(StorageTierSchema, {
    id: `id-${name}`,
    metadata: { name, displayName },
    spec: { description, protocol },
    status: { state },
  });

const renderField = (
  options: Parameters<typeof renderWithProviders>[1],
  initialTier: ResourceSelectValue = emptyResourceSelectValue(),
  isLocked = false,
  protocol?: StorageProtocol,
) =>
  renderWithProviders(
    <Formik initialValues={{ tier: initialTier, other: 'keep-me' }} onSubmit={() => undefined}>
      {({ values }) => (
        <>
          <StorageTierSelectField
            name="tier"
            label="Storage tier"
            fieldId="tier"
            isLocked={isLocked}
            protocol={protocol}
          />
          <output aria-label="selected-tier">{JSON.stringify(values.tier)}</output>
          <output data-other>{values.other}</output>
        </>
      )}
    </Formik>,
    options,
  );

describe('StorageTierSelectField', () => {
  it('stores id and name when a tier is selected', async () => {
    const { user } = renderField({
      apiFixtures: {
        publicStorageTiers: [
          makeTier('fast', 'Fast SSD', 'low latency'),
          makeTier('bulk', 'Bulk', 'cheap'),
        ],
      },
    });

    const toggle = await screen.findByLabelText(/^Storage tier/);
    await user.click(toggle);
    await user.click(await screen.findByRole('option', { name: 'bulk' }));

    await waitFor(() => {
      expect(screen.getByLabelText('selected-tier')).toHaveTextContent(
        '{"id":"id-bulk","name":"bulk"}',
      );
    });
    expect(toggle).toHaveTextContent('bulk');
  });

  it('lists only active tiers', async () => {
    const { user } = renderField({
      apiFixtures: {
        publicStorageTiers: [
          makeTier('fast', 'Fast SSD', 'low latency'),
          makeTier('bulk', 'Bulk', 'cheap'),
          makeTier('gone', 'Retired', '', StorageTierState.UNSPECIFIED),
        ],
      },
    });

    await user.click(await screen.findByLabelText(/^Storage tier/));

    const options = await screen.findAllByRole('option');
    expect(options).toHaveLength(2);
    expect(screen.getByRole('option', { name: 'fast' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'gone' })).not.toBeInTheDocument();
  });

  it('requests active tiers for the requested storage protocol', async () => {
    let capturedFilter: string | undefined;
    const blockTier = makeTier(
      'block',
      'Block',
      'block storage',
      StorageTierState.ACTIVE,
      StorageProtocol.BLOCK,
    );
    const nfsTier = makeTier(
      'nfs',
      'NFS',
      'file storage',
      StorageTierState.ACTIVE,
      StorageProtocol.NFS,
    );

    renderWithProviders(
      <Formik initialValues={{ tier: emptyResourceSelectValue() }} onSubmit={() => undefined}>
        <StorageTierSelectField
          name="tier"
          label="Storage tier"
          fieldId="tier"
          protocol={StorageProtocol.BLOCK}
        />
      </Formik>,
      {
        transportOverrides: {
          onPublicStorageTierList: (request) => {
            capturedFilter = request.filter;
            return { items: [blockTier, nfsTier] };
          },
        },
      },
    );

    await screen.findByLabelText(/^Storage tier/);
    await waitFor(() => expect(capturedFilter).toBeDefined());
    expect(capturedFilter).toContain(`this.status.state == ${StorageTierState.ACTIVE}`);
    expect(capturedFilter).toContain(`this.spec.protocol == ${StorageProtocol.BLOCK}`);
  });

  it('lists only active tiers for the requested storage protocol', async () => {
    const blockTier = makeTier(
      'block',
      'Block',
      'block storage',
      StorageTierState.ACTIVE,
      StorageProtocol.BLOCK,
    );
    const nfsTier = makeTier(
      'nfs',
      'NFS',
      'file storage',
      StorageTierState.ACTIVE,
      StorageProtocol.NFS,
    );
    const retiredBlockTier = makeTier(
      'retired-block',
      'Retired block',
      'retired block storage',
      StorageTierState.UNSPECIFIED,
      StorageProtocol.BLOCK,
    );
    const { user } = renderField(
      {
        apiFixtures: { publicStorageTiers: [blockTier, nfsTier, retiredBlockTier] },
      },
      emptyResourceSelectValue(),
      false,
      StorageProtocol.BLOCK,
    );

    const toggle = await screen.findByLabelText(/^Storage tier/);
    await user.click(toggle);

    expect(screen.getByRole('option', { name: 'block' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'nfs' })).not.toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'retired-block' })).not.toBeInTheDocument();
  });

  it('shows an empty warning when no tiers are available', async () => {
    renderField({ apiFixtures: { publicStorageTiers: [] } });

    expect(await screen.findByText('No storage tiers available')).toBeInTheDocument();
    expect(screen.getByText('Contact your administrator.')).toBeInTheDocument();
    expect(screen.getByLabelText(/^Storage tier/)).toBeDisabled();
  });

  it('shows a load error while preserving other form state', async () => {
    renderField({
      transportOverrides: {
        onPublicStorageTierList: () => {
          throw new ConnectError('boom', Code.Internal);
        },
      },
    });

    expect(await screen.findByText('Failed to load storage tiers')).toBeInTheDocument();
    expect(screen.getByText('boom')).toBeInTheDocument();
    expect(screen.getByText('keep-me', { selector: '[data-other]' })).toBeInTheDocument();
    expect(screen.getByLabelText(/^Storage tier/)).toBeDisabled();
  });

  it('does not show a lock badge by default', async () => {
    renderField({
      apiFixtures: { publicStorageTiers: [makeTier('fast', 'Fast SSD', 'low latency')] },
    });

    await screen.findByLabelText(/^Storage tier/);
    expect(screen.queryByText('Locked by catalog')).not.toBeInTheDocument();
  });

  it('shows the catalog value read-only with a lock badge when isLocked', async () => {
    renderField(
      { apiFixtures: { publicStorageTiers: [makeTier('fast', 'Fast SSD', 'low latency')] } },
      { id: 'id-fast', name: 'fast' },
      true,
    );

    await waitFor(() => {
      expect(screen.getByLabelText(/^Storage tier/)).toHaveTextContent('fast');
    });
    expect(screen.getByText('Locked by catalog')).toBeInTheDocument();
    expect(screen.getByLabelText(/^Storage tier/)).toBeDisabled();
  });
});
