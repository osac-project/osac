import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { type Subnet, SubnetState } from '@osac/types';

import SubnetActionsMenu from './SubnetActionsMenu';
import { renderWithProviders } from '../../test-utils/TestProviders';

const makeSubnet = (overrides: Record<string, unknown> = {}) =>
  ({
    id: 'subnet-1',
    metadata: { name: 'web-a', project: 'web-team' },
    spec: { ipv4Cidr: '10.20.1.0/24', virtualNetwork: { id: 'vn-1' } },
    status: { state: SubnetState.READY },
    ...overrides,
  }) as Subnet;

const renderMenu = (subnet: Subnet, workloadNames?: readonly string[]) =>
  renderWithProviders(<SubnetActionsMenu subnet={subnet} workloadNames={workloadNames} />);

describe('SubnetActionsMenu', () => {
  it('opens the delete confirmation for a deletable subnet', async () => {
    const { user } = renderMenu(makeSubnet(), []);

    await user.click(screen.getByRole('button', { name: 'Actions for web-a' }));
    await user.click(screen.getByRole('menuitem', { name: 'Delete subnet' }));

    expect(screen.getByRole('heading', { name: /Delete web-a\?/ })).toBeInTheDocument();
  });

  it('deletes the subnet through the API', async () => {
    let deletedId: string | undefined;
    const { user } = renderWithProviders(<SubnetActionsMenu subnet={makeSubnet()} />, {
      transportOverrides: {
        onSubnetDelete: (request) => {
          deletedId = request.id;
          return {} as never;
        },
      },
    });

    await user.click(screen.getByRole('button', { name: 'Actions for web-a' }));
    await user.click(screen.getByRole('menuitem', { name: 'Delete subnet' }));
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(deletedId).toBe('subnet-1'));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  });

  it('disables Delete for a default subnet', async () => {
    const { user } = renderMenu(
      makeSubnet({
        metadata: { name: 'default', labels: { 'osac.openshift.io/default': 'true' } },
      }),
    );

    await user.click(screen.getByRole('button', { name: 'Actions for default' }));
    const deleteItem = screen.getByRole('menuitem', { name: 'Delete subnet' });
    expect(deleteItem).toHaveAttribute('aria-disabled', 'true');
    expect(deleteItem).not.toBeDisabled();

    await user.click(deleteItem);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('disables Delete for an auto-created subnet', async () => {
    const { user } = renderMenu(
      makeSubnet({
        metadata: { name: 'auto-subnet', labels: { 'osac.openshift.io/auto-created-for': 'vm-1' } },
      }),
    );

    await user.click(screen.getByRole('button', { name: 'Actions for auto-subnet' }));
    const deleteItem = screen.getByRole('menuitem', { name: 'Delete subnet' });
    expect(deleteItem).toHaveAttribute('aria-disabled', 'true');

    await user.click(deleteItem);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('disables Delete while a workload uses the subnet', async () => {
    const { user } = renderMenu(makeSubnet(), ['vm-a']);

    await user.click(screen.getByRole('button', { name: 'Actions for web-a' }));
    const deleteItem = screen.getByRole('menuitem', { name: 'Delete subnet' });
    expect(deleteItem).toHaveAttribute('aria-disabled', 'true');

    await user.click(deleteItem);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('shows the server guard message and keeps the dialog open when deletion fails', async () => {
    const { user } = renderWithProviders(<SubnetActionsMenu subnet={makeSubnet()} />, {
      transportOverrides: {
        onSubnetDelete: () => {
          throw new ConnectError(
            'subnet is in use by compute instance vm-a',
            Code.FailedPrecondition,
          );
        },
      },
    });

    await user.click(screen.getByRole('button', { name: 'Actions for web-a' }));
    await user.click(screen.getByRole('menuitem', { name: 'Delete subnet' }));
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete' }));

    expect(await screen.findByText('Failed to delete subnet')).toBeInTheDocument();
    expect(screen.getByText(/in use by compute instance vm-a/)).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /Delete web-a\?/ })).toBeInTheDocument();
  });
});
