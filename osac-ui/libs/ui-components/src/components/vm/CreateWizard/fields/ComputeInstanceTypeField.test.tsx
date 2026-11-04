import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { Formik, useFormikContext } from 'formik';
import { describe, expect, it } from 'vitest';

import { InstanceTypeSchema, InstanceTypeState } from '@osac/types';

import ComputeInstanceTypeField from './ComputeInstanceTypeField';
import { renderWithProviders } from '../../../../test-utils/TestProviders';
import type { ResourceSelectValue } from '../../../Form/resourceSelectValue';

const instanceTypes = [
  create(InstanceTypeSchema, {
    id: 'it-small',
    metadata: { name: 'small' },
    spec: { vcpus: 2, memoryGib: 8, state: InstanceTypeState.ACTIVE },
  }),
  create(InstanceTypeSchema, {
    id: 'it-large',
    metadata: { name: 'large' },
    spec: { vcpus: 8, memoryGib: 32, state: InstanceTypeState.ACTIVE },
  }),
];

const FormikValue = () => {
  const { values } = useFormikContext<{ spec: { instanceType: ResourceSelectValue } }>();
  return (
    <output role="status">
      {values.spec.instanceType.id}|{values.spec.instanceType.name}
    </output>
  );
};

const renderField = (
  initialValue: ResourceSelectValue = { id: '', name: '' },
  options?: Parameters<typeof renderWithProviders>[1],
) =>
  renderWithProviders(
    <Formik initialValues={{ spec: { instanceType: initialValue } }} onSubmit={() => undefined}>
      <>
        <ComputeInstanceTypeField name="spec.instanceType" />
        <FormikValue />
      </>
    </Formik>,
    options,
  );

describe('ComputeInstanceTypeField', () => {
  it('loads instance types and stores the selected resource in Formik', async () => {
    const { user } = renderField({ id: '', name: '' }, { apiFixtures: { instanceTypes } });

    await user.click(await screen.findByLabelText(/instance type/i));
    await user.click(await screen.findByRole('option', { name: /large — 8 vCPUs, 32 GiB/ }));

    await waitFor(() => {
      expect(screen.getByRole('combobox')).toHaveValue('large — 8 vCPUs, 32 GiB');
      expect(screen.getByRole('status')).toHaveTextContent('it-large|large');
    });
  });

  it('surfaces a load error and disables the control', async () => {
    renderField(
      { id: '', name: '' },
      {
        transportOverrides: {
          onPublicInstanceTypeList: () => {
            throw new ConnectError('unavailable', Code.Unavailable);
          },
        },
      },
    );

    expect(await screen.findByText('Could not load instance types')).toBeInTheDocument();
    expect(document.getElementById('vm-instance-type')).toHaveClass('pf-m-disabled');
  });

  it('uses the stored resource name for an initial selection', async () => {
    renderField({ id: 'it-small', name: 'small' }, { apiFixtures: { instanceTypes } });

    await waitFor(() => {
      expect(screen.getByRole('combobox')).toHaveValue('small — 2 vCPUs, 8 GiB');
    });
  });

  it('disables the control when requested', async () => {
    renderWithProviders(
      <Formik
        initialValues={{ spec: { instanceType: { id: 'it-small', name: 'small' } } }}
        onSubmit={() => undefined}
      >
        <ComputeInstanceTypeField name="spec.instanceType" isDisabled />
      </Formik>,
      { apiFixtures: { instanceTypes } },
    );

    await waitFor(() => {
      expect(document.getElementById('vm-instance-type')).toHaveClass('pf-m-disabled');
    });
  });
});
