import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { ComputeInstance } from '@osac/types';
import { ComputeInstanceConditionType, ConditionStatus } from '@osac/types';

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

    const { container } = renderWithProviders(<VmStorageSection vm={vm} />);

    expect(screen.getByRole('heading', { name: 'Storage' })).toBeInTheDocument();
    expect(screen.getByText('Boot disk size')).toBeInTheDocument();
    expect(screen.getByText('40 GiB')).toBeInTheDocument();
    expect(screen.getByText('Boot storage tier')).toBeInTheDocument();
    expect(screen.getByText('balanced')).toBeInTheDocument();
    const additionalDisksTable = screen.getByRole('grid', { name: 'Additional disks' });
    expect(additionalDisksTable).toHaveClass('osac-vm-storage-table');
    expect(screen.getByRole('cell', { name: '100 GiB' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'fast' })).toBeInTheDocument();
    expect(screen.getAllByRole('separator')).toHaveLength(1);
    expect(container.querySelector('.pf-v6-c-card')).toBeNull();
  });

  it('renders a hyphen without a table for an empty additional-disk list', () => {
    renderWithProviders(
      <VmStorageSection
        vm={
          {
            spec: { bootDisk: { sizeGib: 40 }, additionalDisks: { items: [] } },
          } as unknown as ComputeInstance
        }
      />,
    );

    const additionalDisksRow = screen.getByText('Additional disks').closest('div') as HTMLElement;

    expect(within(additionalDisksRow).getByText('-')).toBeInTheDocument();
    expect(screen.queryByRole('grid', { name: 'Additional disks' })).not.toBeInTheDocument();
    expect(screen.getAllByRole('separator')).toHaveLength(1);
  });

  it('renders VM conditions below the storage details', () => {
    renderWithProviders(
      <VmStorageSection
        vm={
          {
            status: {
              conditions: [
                {
                  type: ComputeInstanceConditionType.READY,
                  status: ConditionStatus.TRUE,
                  reason: 'AsExpected',
                  message: 'The virtual machine is ready.',
                },
              ],
            },
          } as ComputeInstance
        }
      />,
    );

    expect(screen.getByRole('heading', { name: 'Conditions' })).toBeInTheDocument();
    expect(screen.getByRole('grid', { name: 'Virtual machine conditions' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'READY' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'True' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'AsExpected' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'The virtual machine is ready.' })).toBeInTheDocument();
  });

  it('renders the empty conditions state when the VM has no conditions', () => {
    renderWithProviders(
      <VmStorageSection vm={{ status: { conditions: [] } } as unknown as ComputeInstance} />,
    );

    expect(screen.getByText('No conditions reported.')).toBeInTheDocument();
    expect(
      screen.queryByRole('grid', { name: 'Virtual machine conditions' }),
    ).not.toBeInTheDocument();
  });
});
