import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { DiskImageLifecycle, DiskImageSchema } from '@osac/types';

import BareMetalConfigurationStep from './BareMetalConfigurationStep';
import { createEmptyBareMetalInstanceValues } from './fields';
import { renderWithProviders } from '../../../../../test-utils/TestProviders';

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
        <BareMetalConfigurationStep catalogItem={null} />
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
});
