import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import CatalogStorageTable from './CatalogStorageTable';
import { renderWithProviders } from '../../../test-utils/TestProviders';

describe('CatalogStorageTable', () => {
  it('renders compact additional disk rows', () => {
    renderWithProviders(
      <CatalogStorageTable rows={[{ id: 'disk-1', size: '100 GiB', storageTier: 'fast' }]} />,
    );

    expect(screen.getByRole('columnheader', { name: 'Size' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Storage tier' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: '100 GiB' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'fast' })).toBeInTheDocument();
  });

  it('renders Not set when there are no additional disks', () => {
    renderWithProviders(<CatalogStorageTable rows={[]} />);

    expect(screen.getByRole('cell', { name: 'Not set' })).toBeInTheDocument();
  });
});
