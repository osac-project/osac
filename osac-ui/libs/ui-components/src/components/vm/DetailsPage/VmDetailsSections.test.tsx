import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { ComputeInstance } from '@osac/types';

import VmDetailsSections from './VmDetailsSections';
import { renderWithProviders } from '../../../test-utils/TestProviders';

vi.mock('./VmDetailsCard', () => ({
  default: () => <section aria-label="Details card">Details card</section>,
}));

vi.mock('./VmNetworkingSection', () => ({
  default: () => <section aria-label="Networking">Networking section</section>,
}));

vi.mock('./VmStorageSection', () => ({
  default: () => <section aria-label="Storage">Storage section</section>,
}));

const vm = {
  id: 'vm-1',
  spec: { userDataSource: { source: { case: 'inline', value: '#cloud-config' } } },
} as unknown as ComputeInstance;

describe('VmDetailsSections', () => {
  it('renders the VM details sections as one continuous body', () => {
    const { container } = renderWithProviders(
      <VmDetailsSections
        vm={vm}
        instanceType={undefined}
        isInstanceTypeLoading={false}
        diskImage={undefined}
        isDiskImageLoading={false}
      />,
    );

    expect(screen.getByRole('region', { name: 'Details card' })).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Networking' })).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Storage' })).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'User data' })).not.toBeInTheDocument();
    expect(screen.getAllByRole('separator')).toHaveLength(2);
    expect(container.querySelector('.pf-v6-c-card')).toBeNull();
  });

  it('does not add an extra divider when user data is not configured', () => {
    renderWithProviders(
      <VmDetailsSections
        vm={{ id: 'vm-1', spec: {} } as ComputeInstance}
        instanceType={undefined}
        isInstanceTypeLoading={false}
        diskImage={undefined}
        isDiskImageLoading={false}
      />,
    );

    expect(screen.getAllByRole('separator')).toHaveLength(2);
    expect(screen.queryByRole('region', { name: 'User data' })).not.toBeInTheDocument();
  });

  it('does not add a user data divider for secret-backed user data', () => {
    renderWithProviders(
      <VmDetailsSections
        vm={
          {
            id: 'vm-1',
            spec: {
              userDataSource: { source: { case: 'secret', value: { id: 'secret-1' } } },
            },
          } as ComputeInstance
        }
        instanceType={undefined}
        isInstanceTypeLoading={false}
        diskImage={undefined}
        isDiskImageLoading={false}
      />,
    );

    expect(screen.getAllByRole('separator')).toHaveLength(2);
    expect(screen.queryByRole('region', { name: 'User data' })).not.toBeInTheDocument();
  });
});
