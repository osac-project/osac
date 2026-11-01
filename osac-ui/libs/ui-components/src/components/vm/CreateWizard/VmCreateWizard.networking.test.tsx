import { clone, create } from '@bufbuild/protobuf';
import { Code, ConnectError, type Transport, createRouterTransport } from '@connectrpc/connect';
import { useIsFetching } from '@tanstack/react-query';
import { act, screen, waitFor } from '@testing-library/react';
import { useFormikContext } from 'formik';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  ComputeInstanceCatalogItemSchema,
  ComputeInstanceRunStrategy,
  type ComputeInstancesCreateRequest,
  ComputeInstancesCreateResponseSchema,
  SubnetSchema,
  SubnetState,
  Subnets,
  VirtualNetworkSchema,
  VirtualNetworkState,
} from '@osac/types';

import VmCreateWizard from './VmCreateWizard';
import { useListResource } from '../../../api/use-resource';
import { virtualNetworkFilterForSubnetList } from '../../../api/v1/networking';
import { createMockConnectTransport } from '../../../test-utils/createMockConnectTransport';
import { getResourceSelectCombobox } from '../../../test-utils/resourceSelect';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

vi.mock('./VmReviewStep', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./VmReviewStep')>();
  return {
    ...actual,
    VmReviewStep: () => {
      const { submitForm } = useFormikContext();
      return (
        <>
          <actual.VmReviewStep />
          <button type="button" onClick={() => void submitForm()}>
            Submit form directly
          </button>
        </>
      );
    },
  };
});

const INSTANCE_TYPE_ID = 'it-standard-4-8';
const DISK_IMAGE_ID = 'disk-image-rhel-9';
const STORAGE_TIER_ID = 'tier-fast';

const vmCatalogItem = create(ComputeInstanceCatalogItemSchema, {
  id: 'catalog-vm-1',
  metadata: { name: 'catalog-vm-1' },
  title: 'RHEL 9 small VM',
  description: 'A small RHEL 9 compute instance',
  published: true,
  fields: {
    runStrategy: {
      behavior: {
        case: 'locked',
        value: ComputeInstanceRunStrategy.COMPUTE_INSTANCE_RUN_STRATEGY_HALTED,
      },
    },
    instanceType: {
      behavior: { case: 'locked', value: { id: INSTANCE_TYPE_ID, name: 'standard-4-8' } },
    },
    diskImage: {
      behavior: { case: 'locked', value: { id: DISK_IMAGE_ID, name: 'rhel-9' } },
    },
    bootDisk: {
      sizeGib: { behavior: { case: 'locked', value: 20 } },
      storageTier: {
        behavior: { case: 'locked', value: { id: STORAGE_TIER_ID, name: 'fast' } },
      },
    },
  },
});

const renderWizard = (onClosed = vi.fn()) =>
  renderWithProviders(<VmCreateWizard onClosed={onClosed} />, {
    apiFixtures: { catalogItems: [vmCatalogItem] },
    session: { role: 'tenant-user' },
  });

const clickCatalogItemCard = async (user: ReturnType<typeof renderWizard>['user']) => {
  await screen.findByText('catalog-vm-1');
  const radio = document.getElementById('selectedCatalogItem-catalog-vm-1');
  if (!radio) {
    throw new Error('Catalog item radio input not found');
  }
  await user.click(radio);
};

const selectCatalogItemAndFillName = async (
  user: ReturnType<typeof renderWizard>['user'],
  name: string,
) => {
  await clickCatalogItemCard(user);
  await user.click(screen.getByRole('button', { name: 'Next' }));
  await user.type(await screen.findByRole('textbox', { name: 'Name' }), name);
};

const advanceThroughRemainingSteps = async (user: ReturnType<typeof renderWizard>['user']) => {
  for (let step = 0; step < 5; step += 1) {
    await user.click(screen.getByRole('button', { name: 'Next' }));
  }
  await waitFor(() => {
    expect(screen.getByRole('button', { name: 'Create' })).toBeInTheDocument();
  });
};

const ReviewNetworkQueryControls = () => {
  const query = useListResource(Subnets, { filter: virtualNetworkFilterForSubnetList('vn', true) });
  const fetching = useIsFetching();
  return (
    <>
      <output aria-label="review fetching">{fetching}</output>
      <button type="button" onClick={() => void query.refetch()}>
        Refresh review subnets
      </button>
    </>
  );
};

const renderNetworkWizard = (
  locked: boolean,
  supported: boolean,
  offPage = false,
  alternate = false,
) => {
  const selected = create(SubnetSchema, {
    id: 'selected',
    metadata: { name: 'selected' },
    spec: { virtualNetwork: { id: 'vn' } },
    status: { state: SubnetState.READY, computeInstanceSupported: supported },
  });
  const attachments = {
    items: [
      { virtualNetwork: { id: 'vn', name: 'vn' }, subnet: { id: selected.id, name: 'selected' } },
    ],
  };
  const item = create(ComputeInstanceCatalogItemSchema, vmCatalogItem);
  if (!item.fields) {
    throw new Error('Review catalog fixture requires governed fields');
  }
  item.fields.networkAttachments = create(ComputeInstanceCatalogItemSchema, {
    fields: {
      networkAttachments: {
        behavior: locked
          ? { case: 'locked', value: attachments }
          : { case: 'editable', value: { defaultValue: attachments } },
      },
    },
  }).fields?.networkAttachments;
  let beforeCreate: (() => Promise<void>) | undefined;
  const onCreate = vi.fn(async (request: ComputeInstancesCreateRequest) => {
    await beforeCreate?.();
    return create(ComputeInstancesCreateResponseSchema, {
      object: {
        id: 'review-created',
        metadata: request.object?.metadata,
        spec: request.object?.spec,
      },
    });
  });
  const base = createMockConnectTransport(
    {
      catalogItems: [item],
      virtualNetworks: [
        create(VirtualNetworkSchema, {
          id: 'vn',
          metadata: { name: 'vn' },
          status: { state: VirtualNetworkState.READY },
        }),
      ],
    },
    { onComputeInstanceCreate: onCreate },
  );
  let listCalls = 0;
  let getCalls = 0;
  let beforeGet: (() => Promise<void>) | undefined;
  let selectedMissing = false;
  const other = clone(SubnetSchema, selected);
  other.id = 'alternate';
  if (other.metadata) {
    other.metadata.name = 'alternate';
  }
  if (other.status) {
    other.status.computeInstanceSupported = true;
  }
  const firstPage = Array.from({ length: 100 }, (_, index) =>
    create(SubnetSchema, {
      id: `page-${index}`,
      metadata: { name: `page-${index}` },
      spec: { virtualNetwork: { id: 'vn' } },
      status: { state: SubnetState.READY, computeInstanceSupported: true },
    }),
  );
  const subnetTransport = createRouterTransport((router) => {
    router.service(Subnets, {
      list: (request) => {
        listCalls += 1;
        const eligible = selected.status?.computeInstanceSupported ? [selected] : [];
        if (alternate) {
          eligible.push(other);
        }
        const offset = request.offset ?? 0;
        const items = offPage
          ? [...firstPage, selected].slice(offset, offset + (request.limit || 100))
          : eligible;
        return { items, size: items.length, total: offPage ? 101 : eligible.length };
      },
      get: async (request) => {
        getCalls += 1;
        const object = selectedMissing
          ? undefined
          : clone(SubnetSchema, alternate && request.id === other.id ? other : selected);
        await beforeGet?.();
        return { object };
      },
    });
  });
  const transport: Transport = {
    unary(method, ...args) {
      return (method.parent.typeName === Subnets.typeName ? subnetTransport : base).unary(
        method,
        ...args,
      );
    },
    stream(method, ...args) {
      return base.stream(method, ...args);
    },
  };
  const view = renderWithProviders(
    <>
      <VmCreateWizard onClosed={vi.fn()} />
      <ReviewNetworkQueryControls />
    </>,
    { transport, session: { role: 'tenant-user' } },
  );
  const settle = async (minimumCalls = 1) => {
    await waitFor(() => expect(listCalls).toBeGreaterThanOrEqual(minimumCalls));
    await waitFor(() => expect(screen.getByLabelText('review fetching')).toHaveTextContent('0'));
  };
  const openNetworking = async () => {
    await selectCatalogItemAndFillName(view.user, 'review-vm');
    for (let step = 0; step < 3; step += 1) {
      await view.user.click(screen.getByRole('button', { name: 'Next' }));
    }
    await screen.findByLabelText('Subnet');
    await settle();
  };
  return {
    ...view,
    onCreate,
    selected,
    settle,
    openNetworking,
    deferGet: (pending: () => Promise<void>) => {
      beforeGet = pending;
    },
    deferCreate: (pending: () => Promise<void>) => {
      beforeCreate = pending;
    },
    getCalls: () => getCalls,
    removeSelected: () => {
      selectedMissing = true;
    },
  };
};

describe('VmCreateWizard', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
  });

  it('submits the catalog-governed payload and navigates to the new VM on success', async () => {
    const onComputeInstanceCreate = vi.fn(
      (req: { object?: { metadata?: unknown; spec?: unknown } }) => ({
        object: { id: 'vm-new-1', metadata: req.object?.metadata, spec: req.object?.spec },
      }),
    );
    const { user } = renderWithProviders(<VmCreateWizard onClosed={vi.fn()} />, {
      apiFixtures: { catalogItems: [vmCatalogItem] },
      transportOverrides: { onComputeInstanceCreate: onComputeInstanceCreate as never },
      session: { role: 'tenant-user' },
    });

    await selectCatalogItemAndFillName(user, 'my-vm');
    await advanceThroughRemainingSteps(user);

    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => {
      expect(onComputeInstanceCreate).toHaveBeenCalledTimes(1);
    });
    const request = onComputeInstanceCreate.mock.calls[0][0];
    expect(request.object?.metadata).toMatchObject({ name: 'my-vm' });
    expect(request.object?.spec).toMatchObject({
      catalogItem: { id: 'catalog-vm-1' },
    });
    expect(request.object?.spec).not.toHaveProperty('instanceType');
    expect(request.object?.spec).not.toHaveProperty('diskImage');
    expect(request.object?.spec).not.toHaveProperty('bootDisk');
    expect(request.object?.spec).not.toHaveProperty('runStrategy');

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith('/vms/vm-new-1');
    });
  });

  it('shows a submission error on the review step without navigating away', async () => {
    const { user } = renderWithProviders(<VmCreateWizard onClosed={vi.fn()} />, {
      apiFixtures: { catalogItems: [vmCatalogItem] },
      transportOverrides: {
        onComputeInstanceCreate: () => {
          throw new ConnectError('Instance type is at capacity', Code.ResourceExhausted);
        },
      },
      session: { role: 'tenant-user' },
    });

    await selectCatalogItemAndFillName(user, 'my-vm');
    await advanceThroughRemainingSteps(user);
    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => {
      expect(screen.getByText('Instance type is at capacity')).toBeInTheDocument();
    });
    expect(mockNavigate).not.toHaveBeenCalled();
  });

  it('calls onClosed when Cancel is clicked', async () => {
    const onClosed = vi.fn();
    const { user } = renderWizard(onClosed);

    await user.click(await screen.findByRole('button', { name: 'Cancel' }));

    expect(onClosed).toHaveBeenCalledTimes(1);
  });

  it('blocks Next on the catalog step until a catalog item is selected', async () => {
    const { user } = renderWizard();

    await screen.findByText('catalog-vm-1');
    await user.click(screen.getByRole('button', { name: 'Next' }));

    expect(screen.getByText('Fix the highlighted errors before continuing.')).toBeInTheDocument();
  });

  it('accepts a supported editable subnet through Review and sends its identity (control)', async () => {
    const view = renderNetworkWizard(false, true);
    await view.openNetworking();
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    await view.user.click(await screen.findByRole('button', { name: 'Create' }));
    await waitFor(() => expect(view.onCreate).toHaveBeenCalledTimes(1));
    expect(view.onCreate.mock.calls[0]?.[0]).toMatchObject({
      object: { spec: { networkAttachments: [{ subnet: { id: 'selected' } }] } },
    });
  });

  it.each([['R1 editable supported selection refreshed to false', false, true]] as const)(
    'blocks Next and makes no Create RPC for %s',
    async (_name, locked, supported) => {
      const view = renderNetworkWizard(locked, supported);
      await view.openNetworking();
      if (supported && view.selected.status) {
        view.selected.status.computeInstanceSupported = false;
        await view.user.click(screen.getByRole('button', { name: 'Refresh review subnets' }));
        await view.settle(2);
      }
      await view.user.click(screen.getByRole('button', { name: 'Next' }));
      expect(view.onCreate).not.toHaveBeenCalled();
      expect(screen.queryByRole('button', { name: 'Create' })).not.toBeInTheDocument();
      await screen.findByText('Fix the highlighted errors before continuing.');
      expect(screen.getByLabelText('Subnet')).toBeInTheDocument();
      expect(
        screen.getByText('A subnet must be READY and support compute instances.'),
      ).toBeInTheDocument();
    },
  );

  it('blocks a locked unsupported default before dispatch (negative control)', async () => {
    const view = renderNetworkWizard(true, false);
    await clickCatalogItemCard(view.user);
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    await screen.findByText('Fix the highlighted errors before continuing.');
    expect(view.onCreate).not.toHaveBeenCalled();
  });

  it('corrects an unsupported editable default on Networking and creates once with its replacement', async () => {
    const view = renderNetworkWizard(false, false, false, true);
    await view.openNetworking();
    expect(view.onCreate).not.toHaveBeenCalled();
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    await screen.findByText('Fix the highlighted errors before continuing.');
    await view.user.click(screen.getByLabelText('Subnet'));
    await view.user.click(await screen.findByRole('option', { name: 'alternate' }));
    expect(getResourceSelectCombobox('Subnet')).toHaveValue('alternate');
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    await view.user.click(await screen.findByRole('button', { name: 'Create' }));
    await waitFor(() => expect(view.onCreate).toHaveBeenCalledTimes(1));
    expect(view.onCreate.mock.calls[0]?.[0]).toMatchObject({
      object: { spec: { networkAttachments: [{ subnet: { id: 'alternate' } }] } },
    });
    await waitFor(() => expect(mockNavigate).toHaveBeenCalledOnce());
  });

  it.each([false, true])(
    'does not dispatch twice for repeated Create during pending validation, locked=%s',
    async (locked) => {
      const view = renderNetworkWizard(locked, true);
      await view.openNetworking();
      await view.user.click(screen.getByRole('button', { name: 'Next' }));
      await view.user.click(screen.getByRole('button', { name: 'Next' }));
      let finish: () => void = () => undefined;
      const pending = new Promise<void>((resolve) => {
        finish = resolve;
      });
      let finishCreate: () => void = () => undefined;
      const pendingCreate = new Promise<void>((resolve) => {
        finishCreate = resolve;
      });
      view.deferCreate(() => pendingCreate);
      view.deferGet(() => pending);
      const previous = view.getCalls();
      const createButton = await screen.findByRole('button', { name: 'Create' });
      try {
        await view.user.click(createButton);
        await waitFor(() => expect(view.getCalls()).toBeGreaterThan(previous));
        expect(createButton).toBeDisabled();
        expect(screen.getByRole('button', { name: 'Back' })).toBeDisabled();
        expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled();
        await view.user.click(createButton);
        expect(view.onCreate).not.toHaveBeenCalled();
        await act(async () => {
          finish();
          await pending;
        });
        await waitFor(() => expect(view.onCreate).toHaveBeenCalled());
        expect(mockNavigate).not.toHaveBeenCalled();
        expect(view.onCreate).toHaveBeenCalledTimes(1);
        expect(createButton).toBeDisabled();
      } finally {
        await act(async () => {
          finish();
          finishCreate();
          await pendingCreate;
        });
        await waitFor(() =>
          expect(mockNavigate.mock.calls.length).toBe(view.onCreate.mock.calls.length),
        );
      }
    },
  );

  it.each([false, true])(
    'serializes direct Formik submissions as well as keyboard Create, locked=%s',
    async (locked) => {
      const view = renderNetworkWizard(locked, true);
      await view.openNetworking();
      await view.user.click(screen.getByRole('button', { name: 'Next' }));
      await view.user.click(screen.getByRole('button', { name: 'Next' }));
      let finishGet: () => void = () => undefined;
      const pendingGet = new Promise<void>((resolve) => {
        finishGet = resolve;
      });
      let finishCreate: () => void = () => undefined;
      const pendingCreate = new Promise<void>((resolve) => {
        finishCreate = resolve;
      });
      view.deferGet(() => pendingGet);
      view.deferCreate(() => pendingCreate);
      const previous = view.getCalls();
      try {
        await view.user.click(await screen.findByRole('button', { name: 'Submit form directly' }));
        await waitFor(() => expect(view.getCalls()).toBeGreaterThan(previous));
        await view.user.click(screen.getByRole('button', { name: 'Submit form directly' }));
        expect(view.onCreate).not.toHaveBeenCalled();
        await act(async () => {
          finishGet();
          await pendingGet;
        });
        await waitFor(() => expect(view.onCreate).toHaveBeenCalledTimes(1));
        await view.user.click(screen.getByRole('button', { name: 'Submit form directly' }));
        screen.getByRole('button', { name: /Create$/ }).focus();
        await view.user.keyboard('{Enter}');
        expect(view.onCreate).toHaveBeenCalledTimes(1);
        expect(mockNavigate).not.toHaveBeenCalled();
      } finally {
        await act(async () => {
          finishGet();
          finishCreate();
          await pendingGet;
          await pendingCreate;
        });
        await waitFor(() => expect(mockNavigate).toHaveBeenCalledTimes(1));
      }
    },
  );

  it('releases both guards after Create failure and retries exactly once', async () => {
    const view = renderNetworkWizard(false, true);
    await view.openNetworking();
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    view.deferCreate(() =>
      Promise.reject(new ConnectError('temporary create failure', Code.Unavailable)),
    );
    await view.user.click(await screen.findByRole('button', { name: 'Create' }));
    await screen.findByText('temporary create failure');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Create' })).toBeEnabled());
    expect(view.onCreate).toHaveBeenCalledTimes(1);
    expect(mockNavigate).not.toHaveBeenCalled();
    view.deferCreate(() => Promise.resolve());
    screen.getByRole('button', { name: 'Create' }).focus();
    await view.user.keyboard('{Enter}');
    await waitFor(() => expect(view.onCreate).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(mockNavigate).toHaveBeenCalledTimes(1));
  });

  it.each([1, 3])(
    'recovers Review Get failure at validation boundary %s and creates once on retry',
    async (failureBoundary) => {
      const view = renderNetworkWizard(false, true);
      await view.openNetworking();
      await view.user.click(screen.getByRole('button', { name: 'Next' }));
      await view.user.click(screen.getByRole('button', { name: 'Next' }));
      let failing = true;
      let submitGets = 0;
      view.deferGet(() => {
        submitGets += 1;
        if (failing && submitGets === failureBoundary) {
          return Promise.reject(new ConnectError('review temporary failure', Code.Unavailable));
        }
        return Promise.resolve();
      });
      await view.user.click(await screen.findByRole('button', { name: 'Create' }));
      await waitFor(() => expect(submitGets).toBe(failureBoundary));
      await waitFor(() => expect(screen.getByRole('button', { name: 'Create' })).toBeEnabled());
      expect(view.onCreate).not.toHaveBeenCalled();
      expect(mockNavigate).not.toHaveBeenCalled();
      failing = false;
      await view.user.click(screen.getByRole('button', { name: 'Create' }));
      await waitFor(() => expect(view.onCreate).toHaveBeenCalledTimes(1));
      expect(view.onCreate.mock.calls[0]?.[0].object?.spec?.networkAttachments[0]?.subnet?.id).toBe(
        'selected',
      );
    },
  );

  it('abandons a pending final Get snapshot and retries only the new valid identity', async () => {
    const view = renderNetworkWizard(false, true, false, true);
    await view.openNetworking();
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    let finish: () => void = () => undefined;
    const pending = new Promise<void>((resolve) => {
      finish = resolve;
    });
    let submitGets = 0;
    let reachedFinalGet = false;
    view.deferGet(async () => {
      submitGets += 1;
      if (submitGets === 3) {
        reachedFinalGet = true;
        await pending;
      }
    });
    try {
      await view.user.click(await screen.findByRole('button', { name: 'Create' }));
      await waitFor(() => expect(reachedFinalGet).toBe(true));
      expect(view.onCreate).not.toHaveBeenCalled();
      await view.user.click(screen.getByRole('button', { name: 'Networking' }));
      await view.user.click(screen.getByLabelText('Subnet'));
      await view.user.click(await screen.findByRole('option', { name: 'alternate' }));
      expect(getResourceSelectCombobox('Subnet')).toHaveValue('alternate');
    } finally {
      finish();
    }
    await waitFor(() => expect(screen.getByRole('button', { name: 'Next' })).toBeEnabled());
    expect(view.onCreate).not.toHaveBeenCalled();
    expect(mockNavigate).not.toHaveBeenCalled();
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    await view.user.click(await screen.findByRole('button', { name: 'Create' }));
    await waitFor(() => expect(view.onCreate).toHaveBeenCalledTimes(1));
    expect(view.onCreate.mock.calls[0]?.[0].object?.spec?.networkAttachments[0]?.subnet?.id).toBe(
      'alternate',
    );
  });

  it.each(
    [false, true].flatMap((locked) =>
      ['false', 'unknown', 'notReady', 'missing', 'deleted', 'crossVN', 'wrongID'].map(
        (reason) => ({ locked, reason }),
      ),
    ),
  )(
    'rechecks $reason on Review before final Create, locked=$locked',
    async ({ locked, reason }) => {
      const view = renderNetworkWizard(locked, true);
      await view.openNetworking();
      await view.user.click(screen.getByRole('button', { name: 'Next' }));
      await view.user.click(screen.getByRole('button', { name: 'Next' }));
      await screen.findByRole('button', { name: 'Create' });
      if (!view.selected.status || !view.selected.metadata || !view.selected.spec?.virtualNetwork) {
        throw new Error('Selected fixture requires complete status/metadata/scope');
      }
      if (reason === 'false') {
        view.selected.status.computeInstanceSupported = false;
      }
      if (reason === 'unknown') {
        view.selected.status.computeInstanceSupported = undefined;
      }
      if (reason === 'notReady') {
        view.selected.status.state = SubnetState.PENDING;
      }
      if (reason === 'missing') {
        view.removeSelected();
      }
      if (reason === 'deleted') {
        view.selected.metadata.deletionTimestamp = {
          seconds: 1n,
          nanos: 0,
          $typeName: 'google.protobuf.Timestamp',
        };
      }
      if (reason === 'crossVN') {
        view.selected.spec.virtualNetwork.id = 'wrong-vn';
      }
      if (reason === 'wrongID') {
        view.selected.id = 'wrong-id';
      }
      await view.user.click(screen.getByRole('button', { name: 'Create' }));
      await screen.findByText('Fix the highlighted errors before continuing.');
      expect(view.onCreate).not.toHaveBeenCalled();
    },
  );

  it('R2 advances a valid locked off-page subnet to Review without a Create RPC', async () => {
    const view = renderNetworkWizard(true, true, true);
    await view.openNetworking();
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    await screen.findByRole('button', { name: 'Create' });
    expect(view.onCreate).not.toHaveBeenCalled();
    expect(
      screen.queryByText('Fix the highlighted errors before continuing.'),
    ).not.toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(view.onCreate).toHaveBeenCalledOnce());
    expect(view.onCreate.mock.calls[0]?.[0].object?.spec?.networkAttachments).toEqual([]);
  });
});
