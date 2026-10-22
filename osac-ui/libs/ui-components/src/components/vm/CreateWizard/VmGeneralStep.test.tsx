import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { ComputeInstanceCatalogItemSchema, DiskImageLifecycle, DiskImageSchema } from '@osac/types';

import { getComputeInstanceInitialValues } from './fields';
import { createEmptyComputeInstanceValues } from './payload';
import VmGeneralStep from './VmGeneralStep';
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

describe('VmGeneralStep', () => {
  it('allows the user to select a disk image from the available images', async () => {
    const { user } = renderWithProviders(
      <Formik initialValues={createEmptyComputeInstanceValues()} onSubmit={() => undefined}>
        <VmGeneralStep />
      </Formik>,
      {
        apiFixtures: { diskImages },
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
        <VmGeneralStep />
      </Formik>,
      {
        apiFixtures: { diskImages },
      },
    );

    const diskImageSelect = await screen.findByLabelText(/^Disk image/);
    await waitFor(() => expect(diskImageSelect).toHaveTextContent('rhel'));
    expect(diskImageSelect).toBeDisabled();
  });
});
