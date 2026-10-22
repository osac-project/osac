import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { BareMetalInstanceTypeSchema } from '@osac/types';

import BareMetalInstanceTypeField from './BareMetalInstanceTypeField';
import { renderWithProviders } from '../../../../test-utils/TestProviders';

const instanceTypes = [
  create(BareMetalInstanceTypeSchema, {
    id: 'bm-gpu',
    metadata: { name: 'gpu-large' },
    spec: {
      description: 'GPU worker',
      hardware: {
        cpu: { cores: 64, architecture: 'x86_64', model: 'EPYC', threadsPerCore: 2 },
        memory: { totalGb: 256n, type: 'DDR5' },
        disks: [{ type: 'NVMe', capacityGb: 1000n, interface: 'NVMe' }],
        accelerators: [{ type: 'GPU', model: 'H100', memoryGb: 80 }],
      },
    },
  }),
];

describe('BareMetalInstanceTypeField', () => {
  it('loads instance types, stores the selected name, and shows hardware details', async () => {
    const { user } = renderWithProviders(
      <Formik initialValues={{ spec: { instanceType: { name: '' } } }} onSubmit={() => undefined}>
        <BareMetalInstanceTypeField name="spec.instanceType.name" />
      </Formik>,
      { apiFixtures: { publicBaremetalInstanceTypes: instanceTypes } },
    );

    await user.click(await screen.findByLabelText(/^Instance type/));
    await user.click(await screen.findByRole('option', { name: /gpu-large/ }));

    await waitFor(() => {
      expect(screen.getByLabelText(/^Instance type/)).toHaveTextContent('gpu-large');
      expect(screen.getByText('Model: EPYC')).toBeInTheDocument();
      expect(screen.getByText('GPU (H100, 80 GB)')).toBeInTheDocument();
    });
  });

  it('surfaces a load error with retry', async () => {
    renderWithProviders(
      <Formik initialValues={{ spec: { instanceType: { name: '' } } }} onSubmit={() => undefined}>
        <BareMetalInstanceTypeField name="spec.instanceType.name" />
      </Formik>,
      {
        transportOverrides: {
          onPublicBaremetalInstanceTypeList: () => {
            throw new ConnectError('unavailable', Code.Unavailable);
          },
        },
      },
    );

    expect(await screen.findByText('Could not load instance types')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument();
  });
});
