import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { ComputeInstance } from '@osac/types';
import { ComputeInstanceState } from '@osac/types';

import VmDetails from './VmDetails';
import { renderWithProviders } from '../../../test-utils/TestProviders';

vi.mock('../../../api/v1/instance-types', () => ({
  useInstanceType: vi.fn(),
}));

vi.mock('../../../api/v1/disk-image', () => ({
  useDiskImage: vi.fn(),
}));

vi.mock('./VmDetailsActionButtons', () => ({
  default: ({ onOpenConsole }: { onOpenConsole: VoidFunction }) => (
    <button type="button" onClick={onOpenConsole}>
      Open console
    </button>
  ),
}));

vi.mock('./VmDetailsSections', () => ({
  default: () => <div>VM detail sections</div>,
}));

vi.mock('../../Resource/ResourceDetailHeader', () => ({
  ResourceDetailHeader: ({
    resourceName,
    description,
  }: {
    resourceName: string;
    description?: string;
  }) => (
    <>
      <h1>{resourceName}</h1>
      {description ? <p>{description}</p> : null}
    </>
  ),
}));

vi.mock('../../../VmStatusLabel', () => ({
  VmStatusLabel: () => <span>Running</span>,
}));

const { useInstanceType } = await import('../../../api/v1/instance-types');
const { useDiskImage } = await import('../../../api/v1/disk-image');

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
    vi.mocked(useDiskImage).mockReturnValue({
      data: undefined,
      isLoading: false,
      error: null,
    } as ReturnType<typeof useDiskImage>);
  });

  it('renders the continuous VM details page without a tab strip', () => {
    renderDetails();

    expect(screen.getByText('VM detail sections')).toBeInTheDocument();
    expect(screen.queryByRole('tablist')).not.toBeInTheDocument();
    expect(screen.getAllByRole('separator')).toHaveLength(1);
  });

  it('opens the dedicated console route in a new tab from the header action', async () => {
    const user = userEvent.setup();
    const openWindow = vi.spyOn(window, 'open').mockReturnValue(null);
    renderDetails();

    await user.click(screen.getByRole('button', { name: 'Open console' }));

    expect(openWindow).toHaveBeenCalledWith('/vms/vm-1/console', '_blank', 'noopener,noreferrer');
    openWindow.mockRestore();
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
    expect(screen.queryByText('VM icon')).not.toBeInTheDocument();
    expect(
      screen.getByText(
        'Review configuration, networking, storage, and utilization for this virtual machine.',
      ),
    ).toBeInTheDocument();
    expect(screen.getByText('VM detail sections')).toBeInTheDocument();
  });

  it('shows an alert and still renders the page when disk image lookup fails', () => {
    vi.mocked(useDiskImage).mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new Error('Disk image unavailable'),
    } as ReturnType<typeof useDiskImage>);

    renderDetails();

    expect(screen.getByText('Could not load disk image')).toBeInTheDocument();
    expect(screen.getByText('Disk image unavailable')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'web-01' })).toBeInTheDocument();
    expect(screen.getByText('VM detail sections')).toBeInTheDocument();
  });
});
