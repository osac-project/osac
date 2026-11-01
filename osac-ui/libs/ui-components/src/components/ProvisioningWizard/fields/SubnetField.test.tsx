import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { Button, Wizard, WizardStep } from '@patternfly/react-core';
import { screen, waitFor } from '@testing-library/react';
import { Formik, useFormikContext } from 'formik';
import { describe, expect, it, vi } from 'vitest';

import { SubnetSchema, SubnetState, Subnets } from '@osac/types';

import NetworkFields from './NetworkFields';
import { provisioningFieldValue } from './provisioningFieldValue';
import SubnetField from './SubnetField';
import { useInvalidateServiceQueries } from '../../../api/use-resource';
import { renderWithProviders } from '../../../test-utils/TestProviders';
import { FieldValidationProvider } from '../../Form/FieldValidationContext';
import { OSACWizardFooter } from '../../Wizard/OSACWizardFooter';

const selectedSubnet = () =>
  create(SubnetSchema, {
    id: 'subnet-101',
    metadata: { name: 'selected' },
    spec: { virtualNetwork: { id: 'vn' } },
    status: { state: SubnetState.READY, computeInstanceSupported: true },
  });

const FormControls = () => {
  const invalidate = useInvalidateServiceQueries();
  const { submitForm, isSubmitting, setFieldValue } = useFormikContext();
  return (
    <>
      <Button onClick={() => void invalidate(Subnets)}>Refresh subnets</Button>
      <Button onClick={() => void submitForm()}>Direct submit</Button>
      <Button
        onClick={() =>
          void setFieldValue(
            'network.value.virtualNetwork',
            { id: 'other-vn', name: 'other' },
            false,
          )
        }
      >
        Change VN
      </Button>
      <output>{isSubmitting ? 'Submitting' : 'Idle'}</output>
    </>
  );
};

const setup = (isDisabled = false, requireVmSupport = true) => {
  let current: ReturnType<typeof selectedSubnet> | undefined = selectedSubnet();
  let getError = false;
  let listError = false;
  let pending: Promise<void> | undefined;
  const onSubmit = vi.fn(() => Promise.resolve());
  const onGet = vi.fn(async () => {
    const response = current;
    const failed = getError;
    await pending;
    if (failed) {
      throw new ConnectError('failed', Code.Unavailable);
    }
    return { object: response };
  });
  const page = Array.from({ length: 100 }, (_, index) =>
    create(SubnetSchema, { ...selectedSubnet(), id: `subnet-${index}` }),
  );
  const view = renderWithProviders(
    <Formik
      initialValues={{
        network: provisioningFieldValue({
          useDefaultNetwork: false,
          virtualNetwork: { id: 'vn', name: 'network' },
          subnet: { id: 'subnet-101', name: 'selected' },
          securityGroups: [],
        }),
        externalIp: provisioningFieldValue(false),
      }}
      onSubmit={onSubmit}
      validateOnChange={false}
    >
      <FieldValidationProvider>
        <FormControls />
        <Wizard
          footer={
            <OSACWizardFooter
              onCancel={() => undefined}
              error={null}
              stepHasErrors={(_, errors) => Object.keys(errors).length > 0}
            />
          }
        >
          <WizardStep id="network" name="Networking">
            <NetworkFields
              networkAttachmentName="network"
              externalIpAttachmentName="externalIp"
              requireVmSupport={requireVmSupport}
              isDisabled={isDisabled}
            />
          </WizardStep>
          <WizardStep id="review" name="Review">
            Review selection
          </WizardStep>
        </Wizard>
      </FieldValidationProvider>
    </Formik>,
    {
      apiFixtures: { subnets: page },
      transportOverrides: {
        onSubnetGet: onGet,
        onSubnetList: () => {
          if (listError) {
            throw new ConnectError('failed', Code.Unavailable);
          }
        },
      },
    },
  );
  return {
    ...view,
    onSubmit,
    onGet,
    change: (value: typeof current) => {
      current = value;
    },
    failGet: () => {
      getError = true;
    },
    failList: () => {
      listError = true;
    },
    recover: () => {
      getError = false;
      listError = false;
      current = selectedSubnet();
    },
    defer: (value: Promise<void>) => {
      pending = value;
    },
  };
};

describe('VM subnet selection freshness', () => {
  it.each([false, true])(
    'allows an eligible ID outside the 100 listed items, locked=%s',
    async (locked) => {
      const view = setup(locked);
      await view.user.click(screen.getByRole('button', { name: 'Next' }));
      await screen.findByRole('button', { name: 'Create' });
      await view.user.click(screen.getByRole('button', { name: 'Create' }));
      await waitFor(() => expect(view.onSubmit).toHaveBeenCalledOnce());
      expect(view.onGet).toHaveBeenCalled();
    },
  );

  it.each([false, true])(
    'revalidates changed selections and blocks navigation and submit, locked=%s',
    async (locked) => {
      const view = setup(locked);
      await view.user.click(screen.getByRole('button', { name: 'Direct submit' }));
      await waitFor(() => expect(view.onSubmit).toHaveBeenCalledOnce());
      const invalid = selectedSubnet();
      if (invalid.status) {
        invalid.status.computeInstanceSupported = false;
      }
      view.change(invalid);
      await view.user.click(screen.getByRole('button', { name: 'Refresh subnets' }));
      await view.user.click(screen.getByRole('button', { name: 'Next' }));
      await view.user.click(screen.getByRole('button', { name: 'Direct submit' }));
      await screen.findByText('Fix the highlighted errors before continuing.');
      expect(screen.getByRole('button', { name: 'Next' })).toBeInTheDocument();
      expect(view.onSubmit).toHaveBeenCalledOnce();
    },
  );

  it.each(
    [false, true].flatMap((locked) =>
      ['unknown', 'notReady', 'deleted', 'missing', 'crossVN', 'GETerror', 'listError'].map(
        (reason) => ({ locked, reason }),
      ),
    ),
  )('rejects $reason after refresh, locked=$locked', async ({ locked, reason }) => {
    const view = setup(locked);
    await view.user.click(screen.getByRole('button', { name: 'Direct submit' }));
    await waitFor(() => expect(view.onSubmit).toHaveBeenCalledOnce());
    view.onSubmit.mockClear();
    const invalid = create(SubnetSchema, {
      id: 'subnet-101',
      status: {
        state: reason === 'notReady' ? SubnetState.PENDING : SubnetState.READY,
        computeInstanceSupported: reason === 'unknown' ? undefined : true,
      },
      metadata: {
        name: 'selected',
        deletionTimestamp: reason === 'deleted' ? { seconds: 1n } : undefined,
      },
      spec: { virtualNetwork: { id: reason === 'crossVN' ? 'other-vn' : 'vn' } },
    });
    view.change(reason === 'missing' ? undefined : invalid);
    if (reason === 'GETerror') {
      view.failGet();
    }
    if (reason === 'listError') {
      view.failList();
    }
    await view.user.click(screen.getByRole('button', { name: 'Refresh subnets' }));
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    await screen.findByText('Fix the highlighted errors before continuing.');
    await view.user.click(screen.getByRole('button', { name: 'Direct submit' }));
    expect(view.onSubmit).not.toHaveBeenCalled();
  });

  it('does not submit while selected ID verification is pending', async () => {
    const view = setup();
    let finish!: () => void;
    view.defer(
      new Promise<void>((resolve) => {
        finish = resolve;
      }),
    );
    await view.user.click(screen.getByRole('button', { name: 'Direct submit' }));
    await waitFor(() => expect(view.onGet).toHaveBeenCalled());
    expect(view.onSubmit).not.toHaveBeenCalled();
    finish();
    await waitFor(() => expect(view.onSubmit).toHaveBeenCalledOnce());
    expect(view.onGet.mock.calls.length).toBeLessThan(10);
  });

  it('does not impose VM eligibility on non-VM selectors', async () => {
    const view = setup(false, false);
    view.failGet();
    await view.user.click(screen.getByRole('button', { name: 'Direct submit' }));
    await waitFor(() => expect(view.onSubmit).toHaveBeenCalledOnce());
    expect(view.onGet).not.toHaveBeenCalled();
  });

  it('keeps Next on the current step until ID verification finishes', async () => {
    const view = setup();
    await view.user.click(screen.getByRole('button', { name: 'Direct submit' }));
    await waitFor(() => expect(view.onSubmit).toHaveBeenCalledOnce());
    let finish!: () => void;
    view.defer(
      new Promise<void>((resolve) => {
        finish = resolve;
      }),
    );
    const previousCalls = view.onGet.mock.calls.length;
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    await waitFor(() => expect(view.onGet.mock.calls.length).toBeGreaterThan(previousCalls));
    expect(screen.queryByRole('button', { name: 'Create' })).not.toBeInTheDocument();
    finish();
    await screen.findByRole('button', { name: 'Create' });
    expect(view.onGet.mock.calls.length - previousCalls).toBeLessThan(10);
  });

  it.each(['GETerror', 'listError', 'false'])(
    'recovers from %s after a successful refetch',
    async (reason) => {
      const view = setup();
      if (reason === 'GETerror') {
        view.failGet();
      }
      if (reason === 'listError') {
        view.failList();
      }
      if (reason === 'false') {
        view.change(
          create(SubnetSchema, {
            id: 'subnet-101',
            status: { state: SubnetState.READY, computeInstanceSupported: false },
          }),
        );
      }
      await view.user.click(screen.getByRole('button', { name: 'Refresh subnets' }));
      await view.user.click(screen.getByRole('button', { name: 'Direct submit' }));
      await screen.findAllByText('A subnet must be READY and support compute instances.');
      expect(view.onSubmit).not.toHaveBeenCalled();
      view.recover();
      await view.user.click(screen.getByRole('button', { name: 'Refresh subnets' }));
      await view.user.click(screen.getByRole('button', { name: 'Direct submit' }));
      await waitFor(() => expect(view.onSubmit).toHaveBeenCalledOnce());
    },
  );

  it.each(['chosenIDmismatch', 'VNchange'])('rejects %s', async (reason) => {
    const view = setup();
    await view.user.click(screen.getByRole('button', { name: 'Direct submit' }));
    await waitFor(() => expect(view.onSubmit).toHaveBeenCalledOnce());
    view.onSubmit.mockClear();
    if (reason === 'chosenIDmismatch') {
      view.change(create(SubnetSchema, { ...selectedSubnet(), id: 'different-id' }));
    } else {
      await view.user.click(screen.getByRole('button', { name: 'Change VN' }));
    }
    await view.user.click(screen.getByRole('button', { name: 'Direct submit' }));
    await screen.findAllByText('A subnet must be READY and support compute instances.');
    expect(view.onSubmit).not.toHaveBeenCalled();
  });

  it('does not let an older successful GET override a newer rejection during submission', async () => {
    const view = setup();
    await view.user.click(screen.getByRole('button', { name: 'Direct submit' }));
    await waitFor(() => expect(view.onSubmit).toHaveBeenCalledOnce());
    view.onSubmit.mockClear();
    let finish!: () => void;
    view.defer(
      new Promise<void>((resolve) => {
        finish = resolve;
      }),
    );
    const previousCalls = view.onGet.mock.calls.length;
    await view.user.click(screen.getByRole('button', { name: 'Direct submit' }));
    await waitFor(() => expect(view.onGet.mock.calls.length).toBeGreaterThan(previousCalls));
    view.change(
      create(SubnetSchema, {
        id: 'subnet-101',
        status: { state: SubnetState.READY, computeInstanceSupported: false },
      }),
    );
    view.defer(Promise.resolve());
    await view.user.click(screen.getByRole('button', { name: 'Refresh subnets' }));
    await screen.findAllByText('A subnet must be READY and support compute instances.');
    finish();
    await screen.findByText('Idle');
    expect(view.onSubmit).not.toHaveBeenCalled();
    expect(view.onGet.mock.calls.length - previousCalls).toBeLessThan(10);
  });

  it('lets a newer successful validation supersede a stale failure', async () => {
    const view = setup();
    view.failGet();
    let finish!: () => void;
    view.defer(
      new Promise<void>((resolve) => {
        finish = resolve;
      }),
    );
    await view.user.click(screen.getByRole('button', { name: 'Direct submit' }));
    await waitFor(() => expect(view.onGet).toHaveBeenCalled());
    const previousCalls = view.onGet.mock.calls.length;
    view.recover();
    view.defer(Promise.resolve());
    await view.user.click(screen.getByRole('button', { name: 'Refresh subnets' }));
    await waitFor(() => expect(view.onGet.mock.calls.length).toBeGreaterThan(previousCalls));
    finish();
    await waitFor(() => expect(view.onSubmit).toHaveBeenCalledOnce());
    expect(view.onGet.mock.calls.length - previousCalls).toBeLessThan(10);
  });

  it('allows a valid locked subnet without an optional VN selection', async () => {
    const onSubmit = vi.fn(() => Promise.resolve());
    const view = renderWithProviders(
      <Formik
        initialValues={{ vn: { id: '', name: '' }, subnet: { id: 'subnet-101', name: 'selected' } }}
        onSubmit={onSubmit}
      >
        <>
          <SubnetField name="subnet" virtualNetworkName="vn" requireVmSupport isDisabled />
          <FormControls />
        </>
      </Formik>,
      { transportOverrides: { onSubnetGet: () => ({ object: selectedSubnet() }) } },
    );
    await view.user.click(screen.getByRole('button', { name: 'Direct submit' }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledOnce());
  });
});
