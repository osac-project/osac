import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  ComputeInstanceCatalogItemSchema,
  DiskImageSchema,
  GuestOSFamily,
  InstanceTypeSchema,
  InstanceTypeState,
} from '@osac/types';

import { CatalogItemDetailContent } from './CatalogItemDetailContent';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const INSTANCE_TYPE_ID = 'it-4-8';

const catalogApiFixtures = {
  instanceTypes: [
    create(InstanceTypeSchema, {
      id: INSTANCE_TYPE_ID,
      metadata: { name: 'standard-4-8' },
      spec: { vcpus: 4, memoryGib: 8, state: InstanceTypeState.ACTIVE },
    }),
  ],
  diskImages: [
    create(DiskImageSchema, {
      id: 'di-rhel-10',
      metadata: { name: 'RHEL 10' },
      spec: { guestOsFamily: GuestOSFamily.GUEST_OS_FAMILY_LINUX },
    }),
  ],
};

const vmItem = create(ComputeInstanceCatalogItemSchema, {
  id: 'catalog-rhel-9',
  metadata: {
    name: 'catalog-rhel-9',
    labels: { 'run-strategy': 'Always' },
    project: 'foo',
    tenant: 'foo',
  },
  title: 'RHEL 9 catalog',
  description: 'RHEL 9 base image',
  published: true,
  fields: {
    instanceType: {
      behavior: { case: 'locked', value: { id: INSTANCE_TYPE_ID, name: 'standard-4-8' } },
    },
    diskImage: {
      behavior: { case: 'editable', value: {} },
    },
  },
});

describe('CatalogItemDetailContent', () => {
  it('shows catalog details, labels, and governed configuration', async () => {
    renderWithProviders(<CatalogItemDetailContent item={vmItem} />, {
      apiFixtures: catalogApiFixtures,
    });

    expect(screen.getByText('catalog-rhel-9')).toBeInTheDocument();
    expect(screen.getByText('RHEL 9 base image')).toBeInTheDocument();
    expect(screen.getByText('run-strategy')).toBeInTheDocument();

    await waitFor(() => {
      expect(screen.getByText('4 vCPU')).toBeInTheDocument();
    });
    expect(screen.getAllByText('Locked').length).toBeGreaterThan(0);
    expect(screen.getByText('Editable')).toBeInTheDocument();
  });
});
