import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { ComputeInstance } from '@osac/types';

import VmStorageCard from './VmStorageCard';
import { renderWithProviders } from '../../../test-utils/TestProviders';

describe('VmStorageCard', () => {
  it('lists the boot disk and additional disks with their storage properties', () => {
    const vm = {
      spec: {
        bootDisk: { sizeGib: 40, storageTier: { id: 'tier-balanced', name: 'balanced' } },
        additionalDisks: {
          items: [
            { sizeGib: 100, storageTier: { name: 'fast' } },
            { sizeGib: 20, storageTier: { name: 'capacity' } },
          ],
        },
      },
    } as ComputeInstance;

    renderWithProviders(<VmStorageCard vm={vm} />);

    expect(screen.getByText('Storage')).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Name' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Size' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Storage tier' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'Boot disk' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: '40 GB' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'balanced' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'Additional disk 1' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: '100 GB' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'fast' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'Additional disk 2' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: '20 GB' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'capacity' })).toBeInTheDocument();
  });
});
