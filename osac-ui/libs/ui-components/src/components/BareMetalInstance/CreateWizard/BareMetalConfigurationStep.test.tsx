import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import {
  BareMetalInstanceCatalogItemSchema,
  DiskImageLifecycle,
  DiskImageSchema,
  SecretSchema,
  SecretType,
} from '@osac/types';

import BareMetalConfigurationStep from './BareMetalConfigurationStep';
import { createEmptyBareMetalInstanceValues, getBareMetalInstanceInitialValues } from './fields';
import { renderWithProviders } from '../../../test-utils/TestProviders';
import { provisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';

const diskImages = [
  create(DiskImageSchema, {
    id: 'disk-image-fedora',
    metadata: { name: 'fedora' },
    spec: { lifecycle: DiskImageLifecycle.AVAILABLE, sourceRef: 'quay.io/fedora:latest' },
  }),
  create(DiskImageSchema, {
    id: 'disk-image-rhel',
    metadata: { name: 'rhel' },
    spec: { lifecycle: DiskImageLifecycle.AVAILABLE, sourceRef: 'quay.io/rhel:latest' },
  }),
];

describe('BareMetalConfigurationStep', () => {
  it('allows the user to select a disk image from the available images', async () => {
    const { user } = renderWithProviders(
      <Formik initialValues={createEmptyBareMetalInstanceValues()} onSubmit={() => undefined}>
        <BareMetalConfigurationStep />
      </Formik>,
      {
        apiFixtures: { diskImages },
        transportOverrides: {
          onBaremetalInstanceTypeList: () => ({ items: [], size: 0, total: 0 }),
        },
      },
    );

    const diskImageSelect = await screen.findByLabelText(/^Disk image/);
    await user.click(diskImageSelect);

    expect(screen.getByRole('option', { name: /fedora/ })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: /rhel/ })).toBeInTheDocument();

    await user.click(screen.getByRole('option', { name: /rhel/ }));

    await waitFor(() => expect(diskImageSelect).toHaveTextContent('rhel'));
  });

  it('shows the catalog item disk image when the wizard opens', async () => {
    const catalogItem = create(BareMetalInstanceCatalogItemSchema, {
      id: 'catalog-bm-1',
      fields: {
        diskImage: {
          behavior: {
            case: 'locked',
            value: { id: 'disk-image-rhel', name: 'rhel' },
          },
        },
      },
    });

    renderWithProviders(
      <Formik
        initialValues={getBareMetalInstanceInitialValues(catalogItem)}
        onSubmit={() => undefined}
      >
        <BareMetalConfigurationStep />
      </Formik>,
      {
        apiFixtures: { diskImages },
        transportOverrides: {
          onBaremetalInstanceTypeList: () => ({ items: [], size: 0, total: 0 }),
        },
      },
    );

    const diskImageSelect = await screen.findByLabelText(/^Disk image/);
    await waitFor(() => expect(diskImageSelect).toHaveTextContent('rhel'));
    expect(diskImageSelect).toBeDisabled();
  });

  it('disables instance type selection when the catalog item locks its value', async () => {
    renderWithProviders(
      <Formik initialValues={createEmptyBareMetalInstanceValues()} onSubmit={() => undefined}>
        <BareMetalConfigurationStep />
      </Formik>,
      {
        apiFixtures: { diskImages },
        transportOverrides: {
          onBaremetalInstanceTypeList: () => ({ items: [], size: 0, total: 0 }),
        },
      },
    );

    const instanceTypeSelect = await screen.findByLabelText('Instance type');
    expect(instanceTypeSelect).toBeDisabled();
  });

  it('lets the user switch between inline user data and a USER_DATA Secret', async () => {
    const values = createEmptyBareMetalInstanceValues();
    values.spec.userData = provisioningFieldValue('inline data');
    values.spec.userDataSecret = provisioningFieldValue('stale-secret');

    const { user } = renderWithProviders(
      <Formik initialValues={values} onSubmit={() => undefined}>
        <BareMetalConfigurationStep />
      </Formik>,
      {
        apiFixtures: {
          secrets: [
            create(SecretSchema, {
              id: 'user-data-secret-id',
              metadata: { name: 'user-data-secret', project: '' },
              data: {},
              type: SecretType.USER_DATA,
            }),
          ],
          diskImages,
        },
        transportOverrides: {
          onBaremetalInstanceTypeList: () => ({ items: [], size: 0, total: 0 }),
        },
      },
    );

    await screen.findByRole('radiogroup', { name: 'User data' });
    expect(screen.getByRole('textbox')).toHaveValue('inline data');

    await user.click(screen.getByRole('radio', { name: 'Select secret' }));

    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    const secretSelect = await screen.findByRole('button', {
      name: 'Select a user data secret',
    });
    await user.click(secretSelect);
    await user.click(screen.getByRole('option', { name: 'user-data-secret' }));

    await user.click(screen.getByRole('radio', { name: 'Enter value' }));

    expect(screen.getByRole('textbox')).toHaveValue('inline data');
    expect(
      screen.queryByRole('button', { name: 'Select a user data secret' }),
    ).not.toBeInTheDocument();
  });
});
