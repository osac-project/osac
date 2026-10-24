import { screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { ComputeInstance } from '@osac/types';
import { ComputeInstanceState } from '@osac/types';

import VmDetails from './VmDetails';
import { renderWithProviders } from '../../../test-utils/TestProviders';

vi.mock('../../../api/v1/instance-types', () => ({
  useInstanceType: vi.fn(),
}));

vi.mock('../actions/VmActionButtons', () => ({
  default: () => <div>Action buttons</div>,
}));

vi.mock('./VmDetailsSummary', () => ({
  default: () => <div>VM summary</div>,
}));

vi.mock('./VmDetailsOverviewTab', () => ({
  default: () => <div>Overview tab</div>,
}));

vi.mock('./VmNetworkingTab', () => ({
  default: () => <div>Networking tab</div>,
}));

vi.mock('../console/VmConsoleTab', () => ({
  default: () => <div>Console tab</div>,
}));

vi.mock('../../Resource/ResourceDetailHeader', () => ({
  ResourceDetailHeader: ({ resourceName }: { resourceName: string }) => <h1>{resourceName}</h1>,
}));

vi.mock('../../../VmStatusLabel', () => ({
  VmStatusLabel: () => <span>Running</span>,
}));

const { useInstanceType } = await import('../../../api/v1/instance-types');

const vm = {
  id: 'vm-1',
  metadata: { name: 'web-01' },
  spec: { instanceType: { id: 'standard-4-8' } },
  status: { state: ComputeInstanceState.RUNNING },
} as ComputeInstance;

const renderDetails = () => renderWithProviders(<VmDetails vm={vm} />);

describe('VmDetails', () => {
  beforeEach(() => {
    vi.mocked(useInstanceType).mockReturnValue({
      data: undefined,
      isLoading: false,
      error: null,
    } as ReturnType<typeof useInstanceType>);
  });

  it('renders the Console tab alongside Overview and Networking', () => {
    renderDetails();

    expect(screen.getByRole('tab', { name: 'Overview' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Networking' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Console' })).toBeInTheDocument();
  });

  it('shows an alert and still renders the page when instance type lookup fails', () => {
    vi.mocked(useInstanceType).mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new Error('Instance type unavailable'),
    } as ReturnType<typeof useInstanceType>);

    renderDetails();

    expect(screen.getByText('Could not load instance types')).toBeInTheDocument();
    expect(screen.getByText('Instance type unavailable')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'web-01' })).toBeInTheDocument();
    expect(screen.getByText('VM summary')).toBeInTheDocument();
  });
});
