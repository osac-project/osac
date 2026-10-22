import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { InstanceTypeSchema, InstanceTypeState } from '@osac/types';

import ComputeInstanceTypeField from './ComputeInstanceTypeField';
import { renderWithProviders } from '../../../../test-utils/TestProviders';

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

const renderField = (initialValue = '', options?: Parameters<typeof renderWithProviders>[1]) =>
  renderWithProviders(
    <Formik initialValues={{ spec: { instanceType: initialValue } }} onSubmit={() => undefined}>
      <ComputeInstanceTypeField name="spec.instanceType" />
    </Formik>,
    options,
  );

describe('ComputeInstanceTypeField', () => {
  it('loads instance types and stores the selected id in Formik', async () => {
    const { user } = renderField('', { apiFixtures: { instanceTypes } });

    await user.click(await screen.findByLabelText(/instance type/i));
    await user.click(await screen.findByRole('option', { name: /large — 8 vCPUs, 32 GiB/ }));

    await waitFor(() => {
      expect(screen.getByLabelText(/instance type/i)).toHaveTextContent('large — 8 vCPUs, 32 GiB');
    });
  });

  it('surfaces a load error with retry', async () => {
    renderField('', {
      transportOverrides: {
        onPublicInstanceTypeList: () => {
          throw new ConnectError('unavailable', Code.Unavailable);
        },
      },
    });

    expect(await screen.findByText('Could not load instance types')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument();
  });

  it('resolves a catalog instance type name to the matching option id', async () => {
    renderField('small', { apiFixtures: { instanceTypes } });

    await waitFor(() => {
      expect(screen.getByLabelText(/instance type/i)).toHaveTextContent('small — 2 vCPUs, 8 GiB');
    });
  });

  it('disables the control when requested', async () => {
    renderWithProviders(
      <Formik initialValues={{ spec: { instanceType: 'it-small' } }} onSubmit={() => undefined}>
        <ComputeInstanceTypeField name="spec.instanceType" isDisabled />
      </Formik>,
      { apiFixtures: { instanceTypes } },
    );

    await waitFor(() => {
      expect(screen.getByLabelText(/instance type/i)).toBeDisabled();
    });
  });
});
