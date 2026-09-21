import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import type { UserEvent } from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';

import {
  BareMetalInstanceTypeSchema,
  type BareMetalInstanceType as PrivateBareMetalInstanceType,
} from '@osac/types/private';

import AdminBareMetalInstanceTypeCreatePage from './AdminBareMetalInstanceTypeCreatePage';
import {
  type MockApiFixtures,
  type MockTransportOverrides,
} from '../../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const LIST_ROUTE = '/admin/infrastructure/baremetal-instance-types';

const makeBareMetalInstanceType = (id: string): PrivateBareMetalInstanceType =>
  create(BareMetalInstanceTypeSchema, {
    id,
    metadata: { name: `bm-type-${id}` },
    spec: {
      description: `${id} description`,
      hardware: {
        cpu: { cores: 64, architecture: 'x86_64', model: 'EPYC', threadsPerCore: 2 },
        memory: { totalGb: 256n, type: 'DDR5' },
        disks: [{ type: 'NVMe', capacityGb: 1000n, interface: 'NVMe' }],
        accelerators: [],
        networkPorts: [],
        capabilities: {},
      },
      hostLabelSelector: { matchLabels: { tier: 'gpu' } },
    },
  });

const renderAt = (
  entry: string,
  options: { apiFixtures?: MockApiFixtures; transportOverrides?: MockTransportOverrides } = {},
) =>
  renderWithProviders(
    <Routes>
      <Route path={LIST_ROUTE} element={<h1>List page</h1>} />
      <Route path={`${LIST_ROUTE}/create`} element={<AdminBareMetalInstanceTypeCreatePage />} />
      <Route path={`${LIST_ROUTE}/:id/edit`} element={<AdminBareMetalInstanceTypeCreatePage />} />
    </Routes>,
    { routerEntries: [entry], ...options },
  );

const clickNext = async (user: UserEvent) => {
  const [next] = screen.getAllByRole('button', { name: 'Next' });
  await user.click(next);
};

const fillGeneralStep = async (user: UserEvent, name: string) => {
  await user.type(screen.getByRole('textbox', { name: 'Name' }), name);
  await user.type(screen.getByRole('textbox', { name: 'Host labels key 1' }), 'tier');
  await user.type(screen.getByRole('textbox', { name: 'Host labels value 1' }), 'gpu');
};

const fillCpuMemoryStep = async (user: UserEvent) => {
  await user.type(await screen.findByRole('spinbutton', { name: 'Cores' }), '64');
  await user.type(screen.getByRole('textbox', { name: 'Architecture' }), 'x86_64');
  await user.type(screen.getByRole('spinbutton', { name: 'Threads per core' }), '2');
  await user.type(screen.getByRole('textbox', { name: 'Total (GB)' }), '256');
};

describe('AdminBareMetalInstanceTypeFormPage — create wizard', () => {
  it('blocks advancing past General until the host label selector has a key/value pair', async () => {
    const { user } = renderAt(`${LIST_ROUTE}/create`);

    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'bm-new');
    await clickNext(user);

    expect(
      await screen.findByText('Host label keys are required and must be unique'),
    ).toBeInTheDocument();
    // still on the General step
    expect(screen.getByRole('textbox', { name: 'Name' })).toBeInTheDocument();
  });

  it('shows a host-label validation error for an empty or duplicate key', async () => {
    const { user } = renderAt(`${LIST_ROUTE}/create`);

    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'bm-new');
    await user.type(screen.getByRole('textbox', { name: 'Host labels key 1' }), 'tier');
    await user.click(screen.getByRole('button', { name: 'Add host label' }));
    await clickNext(user);

    expect(
      await screen.findByText('Host label keys are required and must be unique'),
    ).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Host labels key 2' })).toHaveAttribute(
      'aria-describedby',
      'baremetal-instance-type-host-label-selector-1-key-helper-error',
    );

    await user.type(screen.getByRole('textbox', { name: 'Host labels key 2' }), 'tier');
    await clickNext(user);

    expect(screen.getByText('Host label keys are required and must be unique')).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Host labels key 1' })).not.toHaveAttribute(
      'aria-describedby',
    );
    expect(screen.getByRole('textbox', { name: 'Host labels key 2' })).toHaveAttribute(
      'aria-describedby',
      'baremetal-instance-type-host-label-selector-1-key-helper-error',
    );
  });

  it('shows Kubernetes label errors beside the invalid key or value', async () => {
    const { user } = renderAt(`${LIST_ROUTE}/create`);

    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'bm-new');
    await user.type(screen.getByRole('textbox', { name: 'Host labels key 1' }), 'invalid/key!');
    await clickNext(user);

    expect(
      await screen.findByText('Host label key must be a valid Kubernetes label'),
    ).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Host labels key 1' })).toHaveAttribute(
      'aria-describedby',
      'baremetal-instance-type-host-label-selector-0-key-helper-error',
    );

    await user.clear(screen.getByRole('textbox', { name: 'Host labels key 1' }));
    await user.type(screen.getByRole('textbox', { name: 'Host labels key 1' }), 'role');
    await user.type(screen.getByRole('textbox', { name: 'Host labels value 1' }), 'invalid/value');
    await clickNext(user);

    expect(
      await screen.findByText('Host label value must be a valid Kubernetes label value'),
    ).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Host labels value 1' })).toHaveAttribute(
      'aria-describedby',
      'baremetal-instance-type-host-label-selector-0-value-helper-error',
    );
  });

  it('shows an inline alert with the backend message on submission failure', async () => {
    const { user } = renderAt(`${LIST_ROUTE}/create`, {
      transportOverrides: {
        onBaremetalInstanceTypeCreate: () => {
          throw new ConnectError('backend blew up', Code.InvalidArgument);
        },
      },
    });

    await fillGeneralStep(user, 'bm-new');
    await clickNext(user);
    await fillCpuMemoryStep(user);
    await clickNext(user);
    await screen.findByRole('heading', { name: 'Accelerators' });
    await clickNext(user);
    await screen.findByRole('heading', { name: 'Disks' });
    await clickNext(user);
    await screen.findByRole('heading', { name: 'Networking' });
    await clickNext(user);
    await screen.findByRole('heading', { name: 'Capabilities' });
    await clickNext(user);
    await screen.findByRole('heading', { name: 'Review' });
    await user.click(screen.getByRole('button', { name: 'Create' }));

    expect(await screen.findByText('Failed to create resource')).toBeInTheDocument();
    expect(screen.getByText(/backend blew up/)).toBeInTheDocument();
  });

  it('clears the submission error when navigating away from the last step', async () => {
    const { user } = renderAt(`${LIST_ROUTE}/create`, {
      transportOverrides: {
        onBaremetalInstanceTypeCreate: () => {
          throw new ConnectError('backend blew up', Code.InvalidArgument);
        },
      },
    });

    await fillGeneralStep(user, 'bm-new');
    await clickNext(user);
    await fillCpuMemoryStep(user);
    await clickNext(user);
    await screen.findByRole('heading', { name: 'Accelerators' });
    await clickNext(user);
    await screen.findByRole('heading', { name: 'Disks' });
    await clickNext(user);
    await screen.findByRole('heading', { name: 'Networking' });
    await clickNext(user);
    await screen.findByRole('heading', { name: 'Capabilities' });
    await clickNext(user);
    await screen.findByRole('heading', { name: 'Review' });
    await user.click(screen.getByRole('button', { name: 'Create' }));

    expect(await screen.findByText('Failed to create resource')).toBeInTheDocument();
    expect(screen.getByText(/backend blew up/)).toBeInTheDocument();

    // Navigate back from the Review (last) step
    await user.click(screen.getByRole('button', { name: 'Back' }));
    await screen.findByRole('heading', { name: 'Capabilities' });

    // Navigate forward to the last step again
    await clickNext(user);
    await screen.findByRole('heading', { name: 'Review' });

    // The error should be cleared
    expect(screen.queryByText('Failed to create resource')).not.toBeInTheDocument();
    expect(screen.queryByText(/backend blew up/)).not.toBeInTheDocument();
  });

  it('adds and removes a disk row on the Disks step', async () => {
    const { user } = renderAt(`${LIST_ROUTE}/create`);

    await fillGeneralStep(user, 'bm-new');
    await clickNext(user);
    await fillCpuMemoryStep(user);
    await clickNext(user);
    await screen.findByRole('heading', { name: 'Accelerators' });
    await clickNext(user);

    await screen.findByRole('heading', { name: 'Disks' });
    expect(screen.getByText('No disks added.')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Add disk' }));
    expect(screen.getByRole('textbox', { name: 'Capacity (GB)' })).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Remove disk' }));
    expect(screen.getByText('No disks added.')).toBeInTheDocument();
  });
});

describe('AdminBareMetalInstanceTypeFormPage — edit wizard', () => {
  it('pre-populates from the fetched type, disables the name, and sends an update with a field mask', async () => {
    let captured: { object?: { id?: string }; updateMask?: { paths?: string[] } } | undefined;
    const { user } = renderAt(`${LIST_ROUTE}/gpu-1/edit`, {
      apiFixtures: { privateBaremetalInstanceTypes: [makeBareMetalInstanceType('gpu-1')] },
      transportOverrides: {
        onBaremetalInstanceTypeUpdate: (req) => {
          captured = req as unknown as typeof captured;
          return { object: req.object };
        },
      },
    });

    const nameInput = await screen.findByRole('textbox', { name: 'Name' });
    await waitFor(() => expect(nameInput).toHaveValue('bm-type-gpu-1'));
    expect(nameInput).toBeDisabled();

    const description = screen.getByRole('textbox', { name: 'Description' });
    await user.clear(description);
    await user.type(description, 'updated description');
    await clickNext(user);

    await screen.findByRole('heading', { name: 'CPU & Memory' });
    await clickNext(user);
    await screen.findByRole('heading', { name: 'Accelerators' });
    await clickNext(user);
    await screen.findByRole('heading', { name: 'Disks' });
    await clickNext(user);
    await screen.findByRole('heading', { name: 'Networking' });
    await clickNext(user);
    await screen.findByRole('heading', { name: 'Capabilities' });
    await clickNext(user);
    await screen.findByRole('heading', { name: 'Review' });
    await user.click(screen.getByRole('button', { name: 'Edit' }));

    await waitFor(() => expect(captured).toBeDefined());

    expect(captured?.object?.id).toBe('gpu-1');
    expect(captured?.updateMask?.paths).toContain('spec.description');
  });
});
