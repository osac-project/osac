import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { DiskImageLifecycle, DiskImageSchema, GuestOSFamily } from '@osac/types';

import DiskImageField from './DiskImageField';
import { renderWithProviders } from '../../../test-utils/TestProviders';
import { emptyResourceSelectValue } from '../../Form/resourceSelectValue';

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

describe('DiskImageField', () => {
  it('stores the selected disk image id and name in Formik', async () => {
    const { user } = renderWithProviders(
      <Formik
        initialValues={{
          spec: {
            diskImage: emptyResourceSelectValue(),
            guestOsFamily: GuestOSFamily.GUEST_OS_FAMILY_LINUX,
          },
        }}
        onSubmit={() => undefined}
      >
        {({ values }) => (
          <>
            <DiskImageField name="spec.diskImage" guestOsFamilyName="spec.guestOsFamily" />
            <output aria-label="selected-image">{JSON.stringify(values.spec.diskImage)}</output>
          </>
        )}
      </Formik>,
      { apiFixtures: { diskImages } },
    );

    const toggle = await screen.findByLabelText(/^Select the image reference/);
    await user.click(toggle);
    await user.click(screen.getByRole('option', { name: /rhel/ }));

    await waitFor(() => {
      expect(screen.getByLabelText('selected-image')).toHaveTextContent(
        '{"id":"disk-image-rhel","name":"rhel"}',
      );
    });
  });
});
