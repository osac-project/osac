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

vi.mock('./VmExternalIpSection', () => ({
  default: () => <section aria-label="External IP">External IP section</section>,
}));

vi.mock('./VmStorageSection', () => ({
  default: () => <section aria-label="Storage">Storage section</section>,
}));

vi.mock('./VmUserDataCard', () => ({
  default: () => <section aria-label="User data">User data section</section>,
}));

const vm = { id: 'vm-1', spec: { userData: '#cloud-config' } } as ComputeInstance;

describe('VmDetailsSections', () => {
  it('renders the VM details sections as one continuous body', () => {
    renderWithProviders(
      <VmDetailsSections
        vm={vm}
        instanceType={undefined}
        isInstanceTypeLoading={false}
        diskImage={undefined}
        isDiskImageLoading={false}
      />,
    );

    expect(screen.getByRole('region', { name: 'Details card' })).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'External IP' })).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Networking' })).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Storage' })).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'User data' })).toBeInTheDocument();
  });
});
