import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { ComputeInstance } from '@osac/types';

import VmStorageSection from './VmStorageSection';
import { renderWithProviders } from '../../../test-utils/TestProviders';

describe('VmStorageSection', () => {
  it('renders boot disk detail rows and additional disks through the compact table', () => {
    const vm = {
      spec: {
        bootDisk: { sizeGib: 40, storageTier: { name: 'balanced' } },
        additionalDisks: {
          items: [{ sizeGib: 100, storageTier: { name: 'fast' } }],
        },
      },
    } as ComputeInstance;

    renderWithProviders(<VmStorageSection vm={vm} />);

    expect(screen.getByRole('heading', { name: 'Storage' })).toBeInTheDocument();
    expect(screen.getByText('Boot disk size')).toBeInTheDocument();
    expect(screen.getByText('40 GB')).toBeInTheDocument();
    expect(screen.getByText('Boot storage tier')).toBeInTheDocument();
    expect(screen.getByText('balanced')).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: '100 GB' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'fast' })).toBeInTheDocument();
  });

  it('renders Not set for an empty additional-disk list', () => {
    renderWithProviders(
      <VmStorageSection
        vm={
          {
            spec: { bootDisk: { sizeGib: 40 }, additionalDisks: { items: [] } },
          } as unknown as ComputeInstance
        }
      />,
    );

    expect(screen.getByRole('cell', { name: 'Not set' })).toBeInTheDocument();
  });
});
