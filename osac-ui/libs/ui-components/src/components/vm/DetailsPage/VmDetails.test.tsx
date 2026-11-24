import { Code, ConnectError } from '@connectrpc/connect';
import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import type { ComputeInstance, InstanceType } from '@osac/types';
import { ComputeInstanceState } from '@osac/types';

import VmDetails from './VmDetails';
import type { MockTransportOverrides } from '../../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../../test-utils/TestProviders';

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

const vm = {
  id: 'vm-1',
  metadata: { name: 'web-01' },
  spec: { instanceType: { id: 'standard-4-8' } },
  status: { state: ComputeInstanceState.RUNNING },
} as ComputeInstance;

const deletingVm = {
  ...vm,
  status: { ...vm.status, state: ComputeInstanceState.DELETING },
} as ComputeInstance;

const instanceType = {
  id: 'standard-4-8',
  metadata: { name: 'Standard 4 vCPU / 8 GiB' },
} as InstanceType;

const renderDetails = (
  transportOverrides?: MockTransportOverrides,
  vmOverride: ComputeInstance = vm,
) =>
  renderWithProviders(<VmDetails vm={vmOverride} />, {
    apiFixtures: { instanceTypes: [instanceType] },
    transportOverrides,
  });

describe('VmDetails', () => {
  it.each(['tenant-admin', 'tenant-user'] as const)(
    'shows one deletion warning below the header divider and preserves action guards for %s',
    (role) => {
      const view = renderWithProviders(<VmDetails vm={deletingVm} />, {
        apiFixtures: { instanceTypes: [instanceType] },
        session: { role },
      });

      const liveRegion = within(view.container).getByRole('list');
      expect(liveRegion).toHaveAttribute('aria-live', 'polite');
      const warning = within(liveRegion).getByRole('listitem');
      expect(within(liveRegion).getAllByRole('listitem')).toHaveLength(1);
      expect(within(warning).getByText('Virtual machine deletion in progress')).toBeInTheDocument();
      expect(
        within(warning).getByText('Information on this page may be outdated.'),
      ).toBeInTheDocument();
      expect(
        screen.getByRole('separator').compareDocumentPosition(warning) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy();
      expect(screen.getByText('VM detail sections')).toBeInTheDocument();
      for (const name of ['Open console', 'Start', 'Stop', 'Restart']) {
        expect(screen.getByRole('button', { name })).toBeDisabled();
      }
      expect(screen.getByRole('button', { name: 'Delete' })).toHaveAttribute(
        'aria-disabled',
        'true',
      );
    },
  );

  it.each([
    ComputeInstanceState.UNSPECIFIED,
    ComputeInstanceState.STARTING,
    ComputeInstanceState.RUNNING,
    ComputeInstanceState.FAILED,
    ComputeInstanceState.STOPPING,
    ComputeInstanceState.STOPPED,
    ComputeInstanceState.PAUSED,
    undefined,
  ])('does not show a deletion warning in state %s', (state) => {
    const view = renderDetails(undefined, {
      ...vm,
      status: state === undefined ? undefined : { ...vm.status, state },
    } as ComputeInstance);

    expect(within(view.container).queryByRole('list')).not.toBeInTheDocument();
    expect(screen.getByText('VM detail sections')).toBeInTheDocument();
  });

  it('only renders the deletion alert stack item while deleting', () => {
    const view = renderDetails();
    expect(within(view.container).queryByRole('list')).not.toBeInTheDocument();

    view.rerender(<VmDetails vm={deletingVm} />);
    const liveRegion = within(view.container).getByRole('list');
    expect(liveRegion).toHaveAttribute('aria-live', 'polite');
    expect(within(liveRegion).getAllByRole('listitem')).toHaveLength(1);
    expect(
      within(liveRegion).getByText('Virtual machine deletion in progress'),
    ).toBeInTheDocument();

    view.rerender(<VmDetails vm={vm} />);
    expect(within(view.container).queryByRole('list')).not.toBeInTheDocument();
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

  it('shows an alert and still renders the page when instance type lookup fails', async () => {
    renderDetails({
      onPublicInstanceTypeGet: () => {
        throw new ConnectError('Instance type unavailable', Code.Unavailable);
      },
    });

    expect(await screen.findByText('Could not load instance types')).toBeInTheDocument();
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

  it('shows an alert and still renders the page when disk image lookup fails', async () => {
    const vmWithDiskImage = {
      ...vm,
      spec: {
        ...vm.spec,
        diskImage: { id: 'disk-rhel', name: 'RHEL 9.4' },
      },
    } as ComputeInstance;

    renderDetails(
      {
        onDiskImageGet: () => {
          throw new ConnectError('Disk image unavailable', Code.Unavailable);
        },
      },
      vmWithDiskImage,
    );

    expect(await screen.findByText('Could not load disk image')).toBeInTheDocument();
    expect(screen.getByText('Disk image unavailable')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'web-01' })).toBeInTheDocument();
    expect(screen.getByText('VM detail sections')).toBeInTheDocument();
  });
});
