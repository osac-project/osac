import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { StorageTierSchema, StorageTierState } from '@osac/types';

import { createEmptyComputeInstanceValues } from './payload';
import { VmStorageStep } from './VmStorageStep';
import { renderWithProviders } from '../../../test-utils/TestProviders';
import { type ResourceSelectValue, emptyResourceSelectValue } from '../../Form/resourceSelectValue';
import { provisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';

const makeTier = (name: string, displayName: string) =>
  create(StorageTierSchema, {
    id: `id-${name}`,
    metadata: { name, displayName },
    status: { state: StorageTierState.ACTIVE },
  });

const renderStep = (
  storageTier: ResourceSelectValue = emptyResourceSelectValue(),
  storageTierLocked = false,
) =>
  renderWithProviders(
    <Formik
      initialValues={{
        ...createEmptyComputeInstanceValues(),
        spec: {
          ...createEmptyComputeInstanceValues().spec,
          bootDisk: {
            sizeGib: provisioningFieldValue(''),
            storageTier: provisioningFieldValue(storageTier, storageTierLocked),
          },
        },
      }}
      onSubmit={() => undefined}
    >
      <VmStorageStep />
    </Formik>,
    {
      apiFixtures: {
        publicStorageTiers: [makeTier('other', 'Other'), makeTier('fast', 'Fast SSD')],
      },
    },
  );

// VmStorageStep only renders the value already held by Formik, so these tests seed `storageTier`
// directly to simulate the post-selection state.
describe('VmStorageStep', () => {
  it('shows a catalog-seeded default as editable when the field allows editing', async () => {
    renderStep({ id: 'id-fast', name: 'fast' });

    await waitFor(() => {
      expect(screen.getByLabelText(/^Storage tier/)).toHaveTextContent('fast');
      expect(screen.getByLabelText(/^Storage tier/)).not.toBeDisabled();
    });
    expect(screen.queryByText('Locked by catalog')).not.toBeInTheDocument();
  });

  it('shows a lock badge and disables the field when the catalog policy locks the tier', async () => {
    renderStep({ id: 'id-fast', name: 'fast' }, true);

    await waitFor(() => {
      expect(screen.getByLabelText(/^Storage tier/)).toHaveTextContent('fast');
      expect(screen.getByLabelText(/^Storage tier/)).toBeDisabled();
    });
    expect(screen.getByText('Locked by catalog')).toBeInTheDocument();
  });

  it('leaves the tier picker editable and unset by default', async () => {
    renderStep();

    await waitFor(() => expect(screen.getByLabelText(/^Storage tier/)).not.toBeDisabled());
    expect(screen.getByLabelText(/^Storage tier/)).toHaveTextContent('Select a storage tier');
    expect(screen.queryByText('Locked by catalog')).not.toBeInTheDocument();
  });
});

// AdditionalDisksArrayField owns the add/remove/per-row-edit behavior (covered by its own
// AdditionalDisksArrayField.test.tsx) — these tests only confirm it's actually wired into the
// step, alongside the (independently field-tested) boot disk fields.
describe('VmStorageStep — additional disks', () => {
  it('renders the additional-disks array field with no rows initially', () => {
    renderStep();

    expect(screen.getByText('No additional disks added.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add disk' })).toBeInTheDocument();
  });

  it('adding an additional disk does not affect the boot disk fields', async () => {
    const { user } = renderStep({ id: 'id-fast', name: 'fast' });

    await user.click(screen.getByRole('button', { name: 'Add disk' }));

    await waitFor(() => expect(screen.getAllByLabelText(/^Storage tier/)).toHaveLength(2));
    // Boot disk's picker renders before the additional-disks array field in this step.
    expect(screen.getAllByLabelText(/^Storage tier/)[0]).toHaveTextContent('fast');
  });
});
