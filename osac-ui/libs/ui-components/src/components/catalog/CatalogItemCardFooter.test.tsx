import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  ComputeInstanceCatalogItemSchema,
  ComputeInstanceTemplateReferenceSchema,
} from '@osac/types';

import CatalogItemCardFooter from './CatalogItemCardFooter';
import { renderWithProviders } from '../../test-utils/TestProviders';

const makeCatalogItem = (tenant: string) =>
  create(ComputeInstanceCatalogItemSchema, {
    id: 'catalog-item',
    metadata: { name: 'catalog-item', tenant, creator: 'provider-user' },
    title: 'Catalog item',
    template: create(ComputeInstanceTemplateReferenceSchema, { id: 'template' }),
    published: true,
  });

describe('CatalogItemCardFooter', () => {
  it.each([
    ['shared', 'From provider'],
    ['tenant-a', 'Added by you'],
  ])('shows %s catalog ownership as "%s" for tenant admins', (tenant, label) => {
    renderWithProviders(
      <CatalogItemCardFooter
        catalogItem={makeCatalogItem(tenant)}
        role="tenant-admin"
        isWizardMode
      />,
    );

    expect(screen.getByText(label)).toBeInTheDocument();
    expect(screen.queryByText('provider-user')).not.toBeInTheDocument();
  });
});
