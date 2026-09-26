import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  type ComputeInstanceCatalogItem,
  ComputeInstanceTemplateReferenceSchema,
  DiskImageSchema,
  GuestOSFamily,
  InstanceTypeSchema,
  InstanceTypeState,
} from '@osac/types';
import { getViewTypePrefKey } from '@osac/ui-components/components/Primitives/ViewSwitcher';

import { CATALOG_ITEMS_VIEW_KEY, CatalogItemListSection } from './CatalogItemListSection';
import { renderWithProviders } from '../../test-utils/TestProviders';

const catalogItemsViewPrefKey = getViewTypePrefKey(CATALOG_ITEMS_VIEW_KEY);

vi.mock('@osac/ui-components/hooks/use-session.tsx', () => ({
  useSession: vi.fn(() => ({ role: 'admin', username: 'test-user', tenantId: 'test-tenant' })),
}));

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

const vmCatalogItem: ComputeInstanceCatalogItem = {
  $typeName: 'osac.public.v1.ComputeInstanceCatalogItem',
  id: 'catalog-rhel-9',
  metadata: {
    $typeName: 'osac.public.v1.Metadata',
    displayName: '',
    description: '',
    name: 'catalog-rhel-9',
    annotations: {},
    creator: 'foo',
    labels: { env: 'prod' },
    project: 'foo',
    tenant: 'foo',
    version: 1,
  },
  title: 'RHEL 9 catalog',
  description: 'RHEL 9 base image',
  template: create(ComputeInstanceTemplateReferenceSchema, { id: 'tpl-rhel-9' }),
  published: true,
  fields: {
    instanceType: {
      behavior: {
        case: 'locked',
        value: {
          id: INSTANCE_TYPE_ID,
          name: 'standard-4-8',
          project: 'foo',
          shared: false,
        },
      },
    },
    bootDisk: {
      sizeGib: {
        behavior: {
          case: 'locked',
          value: 40,
        },
      },
    },
    diskImage: {
      behavior: {
        case: 'editable',
        value: {
          defaultValue: {
            id: 'di-rhel-10',
            name: 'RHEL 10',
            project: 'foo',
            shared: false,
          },
        },
      },
    },
  } as ComputeInstanceCatalogItem['fields'],
  templateParameters: {},
};

describe('CatalogItemListSection', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('renders catalog items as cards in card view', async () => {
    renderWithProviders(<CatalogItemListSection items={[vmCatalogItem]} />, {
      apiFixtures: catalogApiFixtures,
    });

    expect(screen.queryByRole('grid')).not.toBeInTheDocument();
    expect(
      screen.getByRole('link', {
        name: vmCatalogItem.metadata?.name,
      }),
    ).toBeInTheDocument();
    expect(screen.getByText('Virtual Machine')).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByText('4 vCPU')).toBeInTheDocument();
    });
    expect(screen.getByText('8 GiB')).toBeInTheDocument();
    expect(screen.getByText('40 GiB')).toBeInTheDocument();
    expect(screen.getByText('foo')).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: `Actions for ${vmCatalogItem.metadata?.name}` }),
    ).toBeInTheDocument();
  });

  it('renders a table of catalog items in list view', async () => {
    localStorage.setItem(catalogItemsViewPrefKey, 'list');

    renderWithProviders(<CatalogItemListSection items={[vmCatalogItem]} />, {
      apiFixtures: catalogApiFixtures,
    });

    expect(screen.getByRole('grid', { name: 'Catalog items' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Name' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Status' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Configuration' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Visibility' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Created' })).toBeInTheDocument();
    expect(
      screen.queryByRole('button', {
        name: `Open catalog item details for ${vmCatalogItem.metadata?.name}`,
      }),
    ).not.toBeInTheDocument();

    expect(screen.getByRole('link', { name: vmCatalogItem.metadata?.name })).toHaveAttribute(
      'href',
      `/catalog/vm/${vmCatalogItem.id}`,
    );
    expect(screen.getByText('Virtual Machine')).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByText('4 vCPU')).toBeInTheDocument();
    });
    expect(screen.getByText('8 GiB')).toBeInTheDocument();
    expect(screen.getByText('40 GiB')).toBeInTheDocument();
    expect(screen.getByText('foo')).toBeInTheDocument();
    expect(screen.queryByText(/env/)).not.toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: `Actions for ${vmCatalogItem.metadata?.name}` }),
    ).toBeInTheDocument();
  });

  it('returns nothing when there are no items to show', () => {
    renderWithProviders(<CatalogItemListSection items={[]} />);

    expect(screen.queryByRole('grid')).not.toBeInTheDocument();
    expect(screen.queryByRole('heading')).not.toBeInTheDocument();
    expect(screen.queryByText(vmCatalogItem.metadata?.name || 'N/A')).not.toBeInTheDocument();
  });
});
