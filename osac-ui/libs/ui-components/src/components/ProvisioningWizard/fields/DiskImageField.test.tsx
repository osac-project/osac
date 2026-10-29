import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { DiskImageLifecycle, DiskImageSchema, GuestOSFamily, MetadataSchema } from '@osac/types';

import DiskImageField from './DiskImageField';
import type {
  MockApiFixtures,
  MockTransportOverrides,
} from '../../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../../test-utils/TestProviders';
import { emptyResourceSelectValue } from '../../Form/resourceSelectValue';

const diskImages = [
  create(DiskImageSchema, {
    id: 'disk-image-fedora',
    metadata: { name: 'fedora' },
    spec: {
      guestOsFamily: GuestOSFamily.GUEST_OS_FAMILY_LINUX,
      lifecycle: DiskImageLifecycle.AVAILABLE,
      sourceRef: 'quay.io/fedora:latest',
    },
  }),
  create(DiskImageSchema, {
    id: 'disk-image-rhel',
    metadata: { name: 'rhel' },
    spec: {
      guestOsFamily: GuestOSFamily.GUEST_OS_FAMILY_LINUX,
      lifecycle: DiskImageLifecycle.AVAILABLE,
      sourceRef: 'quay.io/rhel:latest',
    },
  }),
  create(DiskImageSchema, {
    id: 'disk-image-windows',
    metadata: { name: 'windows' },
    spec: {
      guestOsFamily: GuestOSFamily.GUEST_OS_FAMILY_WINDOWS,
      lifecycle: DiskImageLifecycle.AVAILABLE,
      sourceRef: 'quay.io/windows:latest',
    },
  }),
  create(DiskImageSchema, {
    id: 'disk-image-unspecified',
    metadata: { name: 'unspecified' },
    spec: {
      guestOsFamily: GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED,
      lifecycle: DiskImageLifecycle.AVAILABLE,
      sourceRef: 'quay.io/unspecified:latest',
    },
  }),
];

const filterDiskImagesByGuestOsFamily = (images: typeof diskImages, filter?: string) => {
  const guestOsFamily = filter?.match(/this\.spec\.guest_os_family == (\d+)/)?.[1];
  return guestOsFamily === undefined ||
    Number(guestOsFamily) === GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED
    ? images
    : images.filter((image) => image.spec?.guestOsFamily === Number(guestOsFamily));
};

const renderDiskImageField = (
  apiFixtures: MockApiFixtures,
  transportOverrides: MockTransportOverrides,
) =>
  renderWithProviders(
    <Formik
      initialValues={{
        spec: {
          diskImage: emptyResourceSelectValue(),
          guestOsFamily: GuestOSFamily.GUEST_OS_FAMILY_LINUX,
        },
      }}
      onSubmit={() => undefined}
    >
      <DiskImageField name="spec.diskImage" guestOsFamilyName="spec.guestOsFamily" />
    </Formik>,
    { apiFixtures, transportOverrides },
  );

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

    const toggle = await screen.findByRole('button', { name: 'Select the image reference' });
    await user.click(toggle);
    await user.click(screen.getByRole('option', { name: /rhel/ }));

    await waitFor(() => {
      expect(screen.getByLabelText('selected-image')).toHaveTextContent(
        '{"id":"disk-image-rhel","name":"rhel"}',
      );
    });
  });

  it.each([
    [GuestOSFamily.GUEST_OS_FAMILY_LINUX, 'Linux', 'fedora'],
    [GuestOSFamily.GUEST_OS_FAMILY_WINDOWS, 'Microsoft Windows', 'windows'],
    [GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED, 'Unspecified', 'fedora'],
  ] as const)('filters disk images for %s', async (guestOsFamily, optionName, imageName) => {
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
        <DiskImageField name="spec.diskImage" guestOsFamilyName="spec.guestOsFamily" />
      </Formik>,
      {
        apiFixtures: { diskImages },
        transportOverrides: {
          onDiskImageList: (request) => {
            const items = filterDiskImagesByGuestOsFamily(diskImages, request.filter);
            return { items, size: items.length, total: items.length };
          },
        },
      },
    );

    if (guestOsFamily !== GuestOSFamily.GUEST_OS_FAMILY_LINUX) {
      await user.click(screen.getByText(optionName));
    }

    await user.click(await screen.findByRole('button', { name: 'Select the image reference' }));

    expect(await screen.findByRole('option', { name: imageName })).toBeInTheDocument();
    expect(screen.getAllByRole('option')).toHaveLength(
      guestOsFamily === GuestOSFamily.GUEST_OS_FAMILY_LINUX
        ? 2
        : guestOsFamily === GuestOSFamily.GUEST_OS_FAMILY_WINDOWS
          ? 1
          : 4,
    );
  });

  it('does not show disk image descriptions in typeahead options', async () => {
    const imagesWithDescriptions = diskImages.map((image) =>
      image.id === 'disk-image-fedora'
        ? create(DiskImageSchema, {
            ...image,
            metadata: create(MetadataSchema, {
              name: image.metadata?.name,
              description: 'Fedora disk image description',
            }),
          })
        : image,
    );
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
        <DiskImageField name="spec.diskImage" guestOsFamilyName="spec.guestOsFamily" />
      </Formik>,
      { apiFixtures: { diskImages: imagesWithDescriptions } },
    );

    await user.click(await screen.findByRole('button', { name: 'Select the image reference' }));

    expect(screen.getByRole('option', { name: 'fedora' })).not.toHaveTextContent(
      'Fedora disk image description',
    );
  });

  it('clears an image selected for a different guest OS family', async () => {
    const { user } = renderWithProviders(
      <Formik
        initialValues={{
          spec: {
            diskImage: { id: 'disk-image-fedora', name: 'fedora' },
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

    await user.click(screen.getByText('Microsoft Windows'));

    await waitFor(() => {
      expect(screen.getByLabelText('selected-image')).toHaveTextContent('{"id":"","name":""}');
    });
  });

  it('sets the guest OS family when an image is selected from Unspecified', async () => {
    const { user } = renderWithProviders(
      <Formik
        initialValues={{
          spec: {
            diskImage: emptyResourceSelectValue(),
            guestOsFamily: GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED,
          },
        }}
        onSubmit={() => undefined}
      >
        {({ values }) => (
          <>
            <DiskImageField name="spec.diskImage" guestOsFamilyName="spec.guestOsFamily" />
            <output aria-label="selected-family">{values.spec.guestOsFamily}</output>
          </>
        )}
      </Formik>,
      { apiFixtures: { diskImages } },
    );

    await user.click(await screen.findByRole('button', { name: 'Select the image reference' }));
    await user.click(screen.getByRole('option', { name: 'windows' }));

    await waitFor(() => {
      expect(screen.getByLabelText('selected-family')).toHaveTextContent(
        String(GuestOSFamily.GUEST_OS_FAMILY_WINDOWS),
      );
    });
  });

  it('identifies the selected guest OS family in the empty warning', async () => {
    const windowsOnlyDiskImages = [diskImages[2]];
    renderDiskImageField(
      { diskImages: [diskImages[2]] },
      {
        onDiskImageList: (request) => {
          const items = filterDiskImagesByGuestOsFamily(windowsOnlyDiskImages, request.filter);
          return { items, size: items.length, total: items.length };
        },
      },
    );

    expect(
      await screen.findByText('No disk images available of guest OS family: Linux'),
    ).toBeInTheDocument();
  });

  it('shows the empty warning when no disk images are available', async () => {
    renderDiskImageField({ diskImages: [] }, {});

    expect(
      await screen.findByText('No disk images available of guest OS family: Linux'),
    ).toBeInTheDocument();
  });
});
