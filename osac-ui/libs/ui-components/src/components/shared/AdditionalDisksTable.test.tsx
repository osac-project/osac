import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ComputeInstanceDiskSchema } from '@osac/types';

import AdditionalDisksTable from './AdditionalDisksTable';
import { renderWithProviders } from '../../test-utils/TestProviders';

describe('AdditionalDisksTable', () => {
  it('renders compact additional disk rows', () => {
    renderWithProviders(
      <AdditionalDisksTable
        disks={[
          create(ComputeInstanceDiskSchema, {
            sizeGib: 100,
            storageTier: { id: 'tier-1', name: 'fast' },
          }),
        ]}
      />,
    );

    expect(screen.getByRole('columnheader', { name: 'Size' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Storage tier' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: '100 GiB' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'fast' })).toBeInTheDocument();
  });

  it('renders Not set when there are no additional disks', () => {
    renderWithProviders(<AdditionalDisksTable disks={[]} />);

    expect(screen.getByRole('cell', { name: 'Not set' })).toBeInTheDocument();
  });
});
