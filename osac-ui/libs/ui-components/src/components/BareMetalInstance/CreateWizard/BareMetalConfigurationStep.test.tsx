import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import {
  BareMetalInstanceCatalogItemSchema,
  DiskImageLifecycle,
  DiskImageSchema,
} from '@osac/types';

import BareMetalConfigurationStep from './BareMetalConfigurationStep';
import { createEmptyBareMetalInstanceValues, getBareMetalInstanceInitialValues } from './fields';
import { renderWithProviders } from '../../../test-utils/TestProviders';

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

    const diskImageToggle = await screen.findByRole('button', {
      name: 'Select the image reference',
    });
    await user.click(diskImageToggle);

    expect(screen.getByRole('option', { name: /fedora/ })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: /rhel/ })).toBeInTheDocument();

    await user.click(screen.getByRole('option', { name: /rhel/ }));

    await waitFor(() => expect(screen.getByRole('combobox')).toHaveValue('rhel'));
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

    await waitFor(() => expect(screen.getByRole('combobox')).toHaveValue('rhel'));
  });

  it('disables instance type selection when the catalog item locks its value', async () => {
    const catalogItem = create(BareMetalInstanceCatalogItemSchema, {
      fields: {
        instanceType: {
          behavior: {
            case: 'locked',
            value: { name: 'worker' },
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

    const instanceTypeSelect = await screen.findByRole('button', {
      name: 'Instance type',
    });
    expect(instanceTypeSelect).toBeDisabled();
  });
});
