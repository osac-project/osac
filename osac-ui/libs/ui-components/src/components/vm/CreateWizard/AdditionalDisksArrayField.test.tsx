import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';
import * as yup from 'yup';

import { StorageTierSchema, StorageTierState } from '@osac/types';

import { AdditionalDisksArrayField } from './AdditionalDisksArrayField';
import { createEmptyComputeInstanceValues } from './payload';
import {
  getAllResourceSelectComboboxes,
  getResourceSelectCombobox,
} from '../../../test-utils/resourceSelect';
import { renderWithProviders } from '../../../test-utils/TestProviders';
import { type ResourceSelectValue, emptyResourceSelectValue } from '../../Form/resourceSelectValue';

const makeTier = (name: string, displayName: string) =>
  create(StorageTierSchema, {
    id: `id-${name}`,
    metadata: { name, displayName },
    status: { state: StorageTierState.ACTIVE },
  });

const storageTiers = [makeTier('fast', 'Fast SSD'), makeTier('bulk', 'Bulk Capacity')];

const selectTier = async (
  user: ReturnType<typeof renderWithProviders>['user'],
  toggleIndex: number,
  optionName: string,
) => {
  const toggle = screen.getAllByLabelText(/^Storage tier/)[toggleIndex];
  await user.click(toggle);
  await user.click(await screen.findByRole('option', { name: optionName }));
};

const renderField = (
  initialDisks: { sizeGib: string; storageTier: ResourceSelectValue }[] = [],
  withValidation = false,
) =>
  renderWithProviders(
    <Formik
      initialValues={{
        ...createEmptyComputeInstanceValues(),
        spec: {
          ...createEmptyComputeInstanceValues().spec,
          additionalDisks: { value: initialDisks, locked: false },
        },
      }}
      initialTouched={
        withValidation
          ? { spec: { additionalDisks: { value: [{ storageTier: { id: true, name: true } }] } } }
          : undefined
      }
      validateOnMount={withValidation}
      validationSchema={
        withValidation
          ? yup.object({
              spec: yup.object({
                additionalDisks: yup.object({
                  value: yup.array(
                    yup.object({
                      storageTier: yup.object({
                        id: yup.string().required('Storage tier is required'),
                      }),
                    }),
                  ),
                }),
              }),
            })
          : undefined
      }
      onSubmit={() => undefined}
    >
      <AdditionalDisksArrayField name="spec.additionalDisks.value" />
    </Formik>,
    { apiFixtures: { publicStorageTiers: storageTiers } },
  );

describe('AdditionalDisksArrayField', () => {
  it('shows no rows and an Add disk action when there are no additional disks', () => {
    renderField();

    expect(screen.getByText('No additional disks added.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add disk' })).toBeInTheDocument();
    expect(screen.queryByRole('spinbutton')).not.toBeInTheDocument();
  });

  it('appends a live, editable row with a default size when Add disk is clicked', async () => {
    const { user } = renderField();

    await user.click(screen.getByRole('button', { name: 'Add disk' }));

    expect(screen.queryByText('No additional disks added.')).not.toBeInTheDocument();
    expect(screen.getByRole('spinbutton', { name: 'Size (GiB)' })).toHaveValue(30);
    await waitFor(() => expect(screen.getByLabelText(/^Storage tier/)).toBeInTheDocument());
  });

  it('adds a second row alongside the first, each independently editable', async () => {
    const { user } = renderField();

    await user.click(screen.getByRole('button', { name: 'Add disk' }));
    await user.click(screen.getByRole('button', { name: 'Add disk' }));

    const sizeInputs = screen.getAllByRole('spinbutton', { name: 'Size (GiB)' });
    expect(sizeInputs).toHaveLength(2);

    await user.clear(sizeInputs[0]);
    await user.type(sizeInputs[0], '50');
    await user.clear(sizeInputs[1]);
    await user.type(sizeInputs[1], '100');

    expect(sizeInputs[0]).toHaveValue(50);
    expect(sizeInputs[1]).toHaveValue(100);
  });

  it('lets each row pick its own storage tier independently', async () => {
    const { user } = renderField();

    await user.click(screen.getByRole('button', { name: 'Add disk' }));
    await user.click(screen.getByRole('button', { name: 'Add disk' }));

    await waitFor(() => expect(screen.getAllByLabelText(/^Storage tier/)).toHaveLength(2));
    await selectTier(user, 0, 'fast');
    await selectTier(user, 1, 'bulk');

    const comboboxes = getAllResourceSelectComboboxes(/^Storage tier/);
    expect(comboboxes[0]).toHaveValue('fast');
    expect(comboboxes[1]).toHaveValue('bulk');
  });

  it('clears a required error immediately after selecting an additional disk tier', async () => {
    const { user } = renderField(
      [{ sizeGib: '30', storageTier: emptyResourceSelectValue() }],
      true,
    );

    expect(await screen.findByText('Storage tier is required')).toBeInTheDocument();

    await selectTier(user, 0, 'fast');

    await waitFor(() =>
      expect(screen.queryByText('Storage tier is required')).not.toBeInTheDocument(),
    );
  });

  it('removes a row when its Remove action is clicked', async () => {
    const { user } = renderField();

    await user.click(screen.getByRole('button', { name: 'Add disk' }));
    await user.click(screen.getByRole('button', { name: 'Remove disk' }));

    expect(screen.getByText('No additional disks added.')).toBeInTheDocument();
    expect(screen.queryByRole('spinbutton')).not.toBeInTheDocument();
  });

  it('keeps the remaining row intact when a different row is removed', async () => {
    const { user } = renderField();

    await user.click(screen.getByRole('button', { name: 'Add disk' }));
    const sizeInputs1 = screen.getAllByRole('spinbutton', { name: 'Size (GiB)' });
    await user.clear(sizeInputs1[0]);
    await user.type(sizeInputs1[0], '77');

    await user.click(screen.getByRole('button', { name: 'Add disk' }));

    await user.click(screen.getAllByRole('button', { name: 'Remove disk' })[1]);

    const remaining = screen.getAllByRole('spinbutton', { name: 'Size (GiB)' });
    expect(remaining).toHaveLength(1);
    expect(remaining[0]).toHaveValue(77);
  });

  it('renders a row already present in initial values (e.g. seeded from a catalog default) as editable and removable', async () => {
    const { user } = renderField([{ sizeGib: '40', storageTier: { id: 'id-fast', name: 'fast' } }]);

    expect(screen.queryByText('No additional disks added.')).not.toBeInTheDocument();
    const sizeInput = screen.getByRole('spinbutton', { name: 'Size (GiB)' });
    expect(sizeInput).toHaveValue(40);
    await waitFor(() => expect(getResourceSelectCombobox(/^Storage tier/)).toHaveValue('fast'));

    await user.clear(sizeInput);
    await user.type(sizeInput, '60');
    expect(sizeInput).toHaveValue(60);

    await user.click(screen.getByRole('button', { name: 'Remove disk' }));
    expect(screen.getByText('No additional disks added.')).toBeInTheDocument();
  });
});
