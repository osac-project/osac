import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import {
  type ComputeInstance,
  ComputeInstanceState,
  ComputeInstances,
  type InstanceType,
  InstanceTypeState,
  InstanceTypes,
} from '@osac/types';

import EditInstanceTypeModal from './EditInstanceTypeModal';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const cpu = {
  id: 'cpu-1',
  metadata: { name: 'CPU small' },
  spec: { state: InstanceTypeState.ACTIVE, vcpus: 2, memoryGib: 4 },
} as InstanceType;

const cpuLarge = {
  id: 'cpu-2',
  metadata: { name: 'CPU large' },
  spec: { state: InstanceTypeState.ACTIVE, vcpus: 8, memoryGib: 16 },
} as InstanceType;

const gpu = {
  id: 'gpu-1',
  metadata: { name: 'GPU small' },
  spec: {
    state: InstanceTypeState.ACTIVE,
    vcpus: 4,
    memoryGib: 16,
    gpu: { count: 1, resourceName: 'nvidia.com/gpu' },
  },
} as InstanceType;

const vm = (state: ComputeInstanceState, instanceTypeId: string): ComputeInstance =>
  ({
    id: 'vm-1',
    metadata: { name: 'web-01' },
    status: { state },
    spec: { instanceType: { id: instanceTypeId } },
  }) as ComputeInstance;

const renderWrapper = ({
  instance = vm(ComputeInstanceState.RUNNING, 'cpu-1'),
  types = [cpu, cpuLarge, gpu],
  currentType,
  getMode = 'ready' as 'ready' | 'loading' | 'error',
  onUpdate,
  onList,
  rejectUpdate = false,
}: {
  instance?: ComputeInstance;
  types?: InstanceType[];
  currentType?: InstanceType;
  getMode?: 'ready' | 'loading' | 'error';
  onUpdate?: (request: unknown) => void;
  onList?: (request: { filter?: string }) => void;
  rejectUpdate?: boolean;
} = {}) => {
  let getCalls = 0;
  const transport = createRouterTransport((router) => {
    router.service(InstanceTypes, {
      list: (request) => {
        onList?.(request);
        return { items: types };
      },
      get: () => {
        getCalls += 1;
        if (getMode === 'error') {
          throw new ConnectError('lookup failed', Code.Unavailable);
        }
        if (getMode === 'loading' && getCalls === 1) {
          return new Promise(() => undefined);
        }
        return {
          object: currentType ?? types.find((item) => item.id === instance.spec?.instanceType?.id),
        };
      },
    });
    router.service(ComputeInstances, {
      update: (request) => {
        onUpdate?.(request);
        if (rejectUpdate) {
          throw new ConnectError('cannot change GPU configuration', Code.FailedPrecondition);
        }
        return { object: request.object, warnings: [] };
      },
    });
  });

  const onClose = vi.fn();
  const view = renderWithProviders(<EditInstanceTypeModal vm={instance} onClose={onClose} />, {
    transport,
  });
  return { ...view, onClose, getCalls: () => getCalls };
};

describe('EditInstanceTypeModal wrapper', () => {
  it('disables Save and the select while the current type is loading', async () => {
    renderWrapper({ getMode: 'loading' });

    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
    // Wait until the option list finishes loading so pf-m-disabled is only from
    // the current-type guard, not ResourceSelectField's own list-loading disable.
    await waitFor(() => {
      expect(screen.getByRole('combobox')).toHaveAttribute(
        'placeholder',
        'Select an instance type',
      );
    });
    expect(document.getElementById('edit-instance-type')).toHaveClass('pf-m-disabled');
  });

  it('keeps Save disabled until the selection changes', async () => {
    const { user } = renderWrapper();

    await waitFor(() => {
      expect(screen.getByRole('combobox')).not.toBeDisabled();
    });
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();

    await user.click(screen.getByRole('combobox'));
    await user.click(await screen.findByRole('option', { name: /CPU large/ }));
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled();
  });

  it('includes the current type id when that type is deprecated', async () => {
    const deprecated = {
      ...cpu,
      id: 'cpu-old',
      metadata: { name: 'CPU old' },
      spec: { ...cpu.spec, state: InstanceTypeState.DEPRECATED },
    } as InstanceType;
    let listFilter: string | undefined;

    renderWrapper({
      instance: vm(ComputeInstanceState.RUNNING, 'cpu-old'),
      types: [deprecated, cpuLarge],
      currentType: deprecated,
      onList: (request) => {
        listFilter = request.filter;
      },
    });

    await waitFor(() => {
      expect(listFilter).toContain(`this.spec.state == ${InstanceTypeState.ACTIVE}`);
    });
    expect(listFilter).toContain('this.id == "cpu-old"');
  });

  it('retries when the current type failed to load', async () => {
    const { user, getCalls } = renderWrapper({ getMode: 'error' });

    expect(await screen.findByText('Could not load the current instance type')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
    const callsBeforeRetry = getCalls();
    await user.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(getCalls()).toBeGreaterThan(callsBeforeRetry));
  });

  it('offers only GPU types when the current type has a GPU', async () => {
    const { user } = renderWrapper({
      instance: vm(ComputeInstanceState.RUNNING, 'gpu-1'),
      types: [cpu, gpu],
    });

    await user.click(await screen.findByRole('combobox'));
    expect(await screen.findByRole('option', { name: /GPU small/ })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: /CPU small/ })).not.toBeInTheDocument();
  });

  it('passes the next-start alert for a stopped VM after the selection changes', async () => {
    const { user } = renderWrapper({
      instance: vm(ComputeInstanceState.STOPPED, 'cpu-1'),
    });

    expect(screen.queryByText('Resize will apply on next start.')).not.toBeInTheDocument();
    await user.click(await screen.findByRole('combobox'));
    await user.click(await screen.findByRole('option', { name: /CPU large/ }));
    expect(await screen.findByText('Resize will apply on next start.')).toBeInTheDocument();
  });

  it('shows an inline danger alert when the server rejects the update', async () => {
    const { user } = renderWrapper({
      instance: vm(ComputeInstanceState.RUNNING, 'cpu-1'),
      rejectUpdate: true,
    });

    await user.click(await screen.findByRole('combobox'));
    await user.click(await screen.findByRole('option', { name: /CPU large/ }));
    await user.click(screen.getByRole('button', { name: 'Save' }));

    expect(await screen.findByText('Failed to update the instance type')).toBeInTheDocument();
    expect(screen.getByRole('dialog', { name: 'Edit instance type' })).toBeInTheDocument();
  });

  it('updates with the explicit instance type mask', async () => {
    let request:
      | {
          updateMask?: { paths?: string[] };
          object?: { spec?: { instanceType?: { id?: string } } };
        }
      | undefined;
    const { user, onClose } = renderWrapper({
      onUpdate: (value) => {
        request = value as typeof request;
      },
    });

    await user.click(await screen.findByRole('combobox'));
    await user.click(await screen.findByRole('option', { name: /CPU large/ }));
    expect(screen.queryByText('Resize will apply on next start.')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(request?.updateMask?.paths).toEqual(['spec.instance_type']));
    expect(request?.object?.spec?.instanceType?.id).toBe('cpu-2');
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });
});
