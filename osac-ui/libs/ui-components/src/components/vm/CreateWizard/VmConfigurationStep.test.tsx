import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { ComputeInstanceCatalogItemSchema, DiskImageLifecycle, DiskImageSchema } from '@osac/types';

import { getComputeInstanceInitialValues } from './fields';
import { createEmptyComputeInstanceValues } from './payload';
import { VmConfigurationStep } from './VmConfigurationStep';
import { getResourceSelectInput } from '../../../test-utils/query';
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

describe('VmConfigurationStep', () => {
  it('allows the user to select a disk image from the available images', async () => {
    const { user } = renderWithProviders(
      <Formik initialValues={createEmptyComputeInstanceValues()} onSubmit={() => undefined}>
        <VmConfigurationStep />
      </Formik>,
      {
        apiFixtures: { diskImages },
      },
    );

    expect(screen.getByRole('separator')).toBeInTheDocument();

    const diskImageSelect = await waitFor(() =>
      getResourceSelectInput(/^Select the image reference/),
    );
    await user.click(diskImageSelect);

    expect(screen.getByRole('option', { name: /fedora/ })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: /rhel/ })).toBeInTheDocument();

    await user.click(screen.getByRole('option', { name: /rhel/ }));

    await waitFor(() => expect(diskImageSelect).toHaveValue('rhel'));
  });

  it('shows the catalog item disk image when the wizard opens', async () => {
    const catalogItem = create(ComputeInstanceCatalogItemSchema, {
      id: 'catalog-rhel-9',
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
        initialValues={getComputeInstanceInitialValues(catalogItem)}
        onSubmit={() => undefined}
      >
        <VmConfigurationStep />
      </Formik>,
      {
        apiFixtures: { diskImages },
      },
    );

    await waitFor(() =>
      expect(getResourceSelectInput(/^Select the image reference/)).toHaveValue('rhel'),
    );
  });
});
