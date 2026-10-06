import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  InstanceTypeSchema,
  InstanceTypeState,
  InstanceTypesDeleteResponseSchema,
  InstanceTypesUpdateResponseSchema,
  type InstanceType as PrivateInstanceType,
} from '@osac/types/private';

import AdminInstanceTypeActionsMenu from './AdminInstanceTypeActionsMenu';
import { renderWithProviders } from '../../test-utils/TestProviders';

const makeInstanceType = (state: InstanceTypeState): PrivateInstanceType =>
  create(InstanceTypeSchema, {
    id: 'it-1',
    metadata: { name: 'general-4-16' },
    spec: { vcpus: 4, memoryGib: 16, description: '', state },
  });

const renderMenu = (
  instanceType: PrivateInstanceType,
  options?: Parameters<typeof renderWithProviders>[1],
) => renderWithProviders(<AdminInstanceTypeActionsMenu instanceType={instanceType} />, options);

const openMenu = async (user: ReturnType<typeof renderWithProviders>['user']) => {
  await user.click(screen.getByRole('button', { name: 'Actions for general-4-16' }));
};

describe('AdminInstanceTypeActionsMenu', () => {
  it('exposes Deprecate and Obsolete, but not Reactivate or Delete, for an ACTIVE instance type', async () => {
    const { user } = renderMenu(makeInstanceType(InstanceTypeState.ACTIVE));
    await openMenu(user);

    expect(screen.getByRole('menuitem', { name: 'Deprecate' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Obsolete' })).toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Reactivate' })).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Delete' })).not.toBeInTheDocument();
  });

  it('exposes Obsolete and Reactivate, but not Deprecate or Delete, for a DEPRECATED instance type', async () => {
    const { user } = renderMenu(makeInstanceType(InstanceTypeState.DEPRECATED));
    await openMenu(user);

    expect(screen.getByRole('menuitem', { name: 'Obsolete' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Reactivate' })).toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Deprecate' })).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Delete' })).not.toBeInTheDocument();
  });

  it('exposes Deprecate, Reactivate, and Delete, but not Obsolete, for an OBSOLETE instance type', async () => {
    const { user } = renderMenu(makeInstanceType(InstanceTypeState.OBSOLETE));
    await openMenu(user);

    expect(screen.getByRole('menuitem', { name: 'Deprecate' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Reactivate' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Delete' })).toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Obsolete' })).not.toBeInTheDocument();
  });

  it('exposes Deprecate, Obsolete, and Reactivate, but not Delete, for an unset (UNSPECIFIED) state', async () => {
    const { user } = renderMenu(makeInstanceType(InstanceTypeState.UNSPECIFIED));
    await openMenu(user);

    expect(screen.getByRole('menuitem', { name: 'Deprecate' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Obsolete' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Reactivate' })).toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Delete' })).not.toBeInTheDocument();
  });

  it('sends a spec.state update targeting DEPRECATED when Deprecate is clicked', async () => {
    let captured: Record<string, unknown> | undefined;
    const { user } = renderMenu(makeInstanceType(InstanceTypeState.ACTIVE), {
      transportOverrides: {
        onInstanceTypeUpdate: (req) => {
          captured = req as unknown as Record<string, unknown>;
          return create(InstanceTypesUpdateResponseSchema, {
            object: makeInstanceType(InstanceTypeState.DEPRECATED),
          });
        },
      },
    });
    await openMenu(user);
    await user.click(screen.getByRole('menuitem', { name: 'Deprecate' }));

    await waitFor(() => expect(captured).toBeDefined());
    const object = captured?.object as { id?: string; spec?: { state?: InstanceTypeState } };
    expect(object.id).toBe('it-1');
    expect(object.spec?.state).toBe(InstanceTypeState.DEPRECATED);
  });

  it('shows a toast with the deprecate failure title and backend message when the update fails', async () => {
    const { user } = renderMenu(makeInstanceType(InstanceTypeState.ACTIVE), {
      transportOverrides: {
        onInstanceTypeUpdate: () => {
          throw new ConnectError('deprecation rejected', Code.FailedPrecondition);
        },
      },
    });
    await openMenu(user);
    await user.click(screen.getByRole('menuitem', { name: 'Deprecate' }));

    expect(await screen.findByText('Failed to deprecate instance type')).toBeInTheDocument();
    expect(screen.getByText('deprecation rejected')).toBeInTheDocument();
  });

  it('shows a toast with the reactivate failure title when the reactivate update fails', async () => {
    const { user } = renderMenu(makeInstanceType(InstanceTypeState.DEPRECATED), {
      transportOverrides: {
        onInstanceTypeUpdate: () => {
          throw new ConnectError('reactivation rejected', Code.FailedPrecondition);
        },
      },
    });
    await openMenu(user);
    await user.click(screen.getByRole('menuitem', { name: 'Reactivate' }));

    expect(await screen.findByText('Failed to reactivate instance type')).toBeInTheDocument();
  });

  it('shows a toast with the obsolete failure title when the obsolete update fails', async () => {
    const { user } = renderMenu(makeInstanceType(InstanceTypeState.ACTIVE), {
      transportOverrides: {
        onInstanceTypeUpdate: () => {
          throw new ConnectError('obsolete rejected', Code.FailedPrecondition);
        },
      },
    });
    await openMenu(user);
    await user.click(screen.getByRole('menuitem', { name: 'Obsolete' }));

    expect(await screen.findByText('Failed to mark instance type as obsolete')).toBeInTheDocument();
  });

  it('opens a confirm modal for Delete and deletes the instance type on confirmation', async () => {
    let deleteCalled = false;
    const { user } = renderMenu(makeInstanceType(InstanceTypeState.OBSOLETE), {
      transportOverrides: {
        onInstanceTypeDelete: () => {
          deleteCalled = true;
          return create(InstanceTypesDeleteResponseSchema);
        },
      },
    });
    await openMenu(user);
    await user.click(screen.getByRole('menuitem', { name: 'Delete' }));

    expect(screen.getByRole('dialog')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(deleteCalled).toBe(true);
  });

  it('shows an inline alert in the confirm modal, not a toast, when delete fails', async () => {
    const { user } = renderMenu(makeInstanceType(InstanceTypeState.OBSOLETE), {
      transportOverrides: {
        onInstanceTypeDelete: () => {
          throw new ConnectError('instance type is in use', Code.FailedPrecondition);
        },
      },
    });
    await openMenu(user);
    await user.click(screen.getByRole('menuitem', { name: 'Delete' }));
    await user.click(screen.getByRole('button', { name: 'Delete' }));

    const modal = screen.getByRole('dialog');
    expect(await screen.findByText('Failed to delete instance type')).toBeInTheDocument();
    expect(screen.getByText('instance type is in use')).toBeInTheDocument();
    expect(modal).toBeInTheDocument();
  });
});
