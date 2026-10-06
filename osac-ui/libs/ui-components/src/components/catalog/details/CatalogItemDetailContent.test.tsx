import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { ComputeInstanceCatalogItem } from '@osac/types';

import { CatalogItemDetailContent } from './CatalogItemDetailContent';

vi.mock('../../../hooks/useTranslation', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}));

const vmItem: ComputeInstanceCatalogItem = {
  $typeName: 'osac.public.v1.ComputeInstanceCatalogItem',
  id: 'catalog-rhel-9',
  metadata: {
    $typeName: 'osac.public.v1.Metadata',
    displayName: '',
    description: '',
    name: 'catalog-rhel-9',
    annotations: {},
    creator: 'foo',
    labels: { 'run-strategy': 'Always' },
    project: 'foo',
    tenant: 'foo',
    version: 1,
  },
  title: 'RHEL 9 catalog',
  description: 'RHEL 9 base image',
  published: true,
  template: undefined,
  templateParameters: {},
};

describe('CatalogItemDetailContent', () => {
  it('shows drawer-era details including run-strategy configuration and labels', () => {
    render(<CatalogItemDetailContent item={vmItem} />);

    expect(screen.getByText('catalog-rhel-9')).toBeInTheDocument();
    expect(screen.getByText('RHEL 9 base image')).toBeInTheDocument();
  });
});
