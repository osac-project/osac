import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import {
  SecurityGroupSchema,
  SecurityGroupState,
  SubnetSchema,
  SubnetState,
  VirtualNetworkSchema,
  VirtualNetworkState,
} from '@osac/types';

import type { NetworkAttachmentFormFields } from './networkAttachments';
import NetworkFields from './NetworkFields';
import { type ProvisioningFieldValue, provisioningFieldValue } from './provisioningFieldValue';
import { renderWithProviders } from '../../../test-utils/TestProviders';
import { emptyResourceSelectValue } from '../../Form/resourceSelectValue';

interface TestFormValues {
  spec: {
    networkAttachment: ProvisioningFieldValue<NetworkAttachmentFormFields>;
    autoExternalIpAttachment: ProvisioningFieldValue<boolean>;
  };
}

const emptyTestValues = (useDefaultNetwork = true): TestFormValues => ({
  spec: {
    networkAttachment: provisioningFieldValue({
      useDefaultNetwork,
      virtualNetwork: emptyResourceSelectValue(),
      subnet: emptyResourceSelectValue(),
      securityGroups: [],
    }),
    autoExternalIpAttachment: provisioningFieldValue(false),
  },
});

const renderFields = (initialValues?: TestFormValues, isDisabled = false) =>
  renderWithProviders(
    <Formik initialValues={initialValues ?? emptyTestValues()} onSubmit={() => undefined}>
      <NetworkFields
        networkAttachmentName="spec.networkAttachment"
        externalIpAttachmentName="spec.autoExternalIpAttachment"
        isDisabled={isDisabled}
      />
    </Formik>,
  );

describe('NetworkAttachmentFields', () => {
  it('renders "use default network" and "auto external IP" checkboxes', () => {
    renderFields();

    expect(
      screen.getByRole('checkbox', { name: 'Use tenant default network' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('checkbox', { name: 'Auto External IP Attachment' }),
    ).toBeInTheDocument();
  });

  it('hides VN, Subnet, and Security Groups pickers when default network is checked', () => {
    renderFields();

    expect(screen.getByRole('checkbox', { name: 'Use tenant default network' })).toBeChecked();
    expect(screen.queryByText('Virtual network')).not.toBeInTheDocument();
    expect(screen.queryByText('Subnet')).not.toBeInTheDocument();
    expect(screen.queryByText('Security groups')).not.toBeInTheDocument();
  });

  it('shows VN, Subnet, and Security Groups pickers when default network is unchecked', async () => {
    const user = userEvent.setup();
    renderFields();

    await user.click(screen.getByRole('checkbox', { name: 'Use tenant default network' }));

    expect(screen.getByText('Virtual network')).toBeInTheDocument();
    expect(screen.getByText('Subnet')).toBeInTheDocument();
    expect(screen.getByText('Security groups')).toBeInTheDocument();
  });

  it('hides pickers when toggling default network back on', async () => {
    const user = userEvent.setup();
    renderFields();

    await user.click(screen.getByRole('checkbox', { name: 'Use tenant default network' }));
    expect(screen.getByText('Virtual network')).toBeInTheDocument();

    await user.click(screen.getByRole('checkbox', { name: 'Use tenant default network' }));
    expect(screen.queryByText('Virtual network')).not.toBeInTheDocument();
  });

  it('renders auto external IP helper text when provided', () => {
    renderWithProviders(
      <Formik initialValues={emptyTestValues()} onSubmit={() => undefined}>
        <NetworkFields
          networkAttachmentName="spec.networkAttachment"
          externalIpAttachmentName="spec.autoExternalIpAttachment"
          autoExternalIpHelperText="Provision external IPs automatically."
        />
      </Formik>,
    );

    expect(screen.getByText('Provision external IPs automatically.')).toBeInTheDocument();
  });

  it('does not display validation errors on initial render', () => {
    renderFields(emptyTestValues(false));

    expect(screen.queryByText('Virtual network is required')).not.toBeInTheDocument();
    expect(screen.queryByText('Subnet is required')).not.toBeInTheDocument();
  });

  it('disables the default-network checkbox and pickers together', () => {
    renderFields(emptyTestValues(false), true);

    expect(screen.getByRole('checkbox', { name: 'Use tenant default network' })).toBeDisabled();
    expect(screen.getByLabelText(/^Virtual network/)).toBeDisabled();
    expect(screen.getByLabelText(/^Subnet/)).toBeDisabled();
    expect(screen.getByLabelText(/^Security groups/).closest('.pf-v6-c-menu-toggle')).toHaveClass(
      'pf-m-disabled',
    );
    expect(screen.getByRole('checkbox', { name: 'Auto External IP Attachment' })).toBeDisabled();
  });

  it('disables auto external IP from its own catalog policy', () => {
    renderFields({
      ...emptyTestValues(),
      spec: {
        ...emptyTestValues().spec,
        autoExternalIpAttachment: provisioningFieldValue(false, true),
      },
    });

    expect(screen.getByRole('checkbox', { name: 'Use tenant default network' })).not.toBeDisabled();
    expect(screen.getByRole('checkbox', { name: 'Auto External IP Attachment' })).toBeDisabled();
  });

  it('resolves the parent virtual network from a catalog subnet so attachments display', async () => {
    renderWithProviders(
      <Formik
        initialValues={{
          ...emptyTestValues(false),
          spec: {
            ...emptyTestValues(false).spec,
            networkAttachment: provisioningFieldValue({
              ...emptyTestValues(false).spec.networkAttachment.value,
              subnet: { id: 'subnet-1', name: 'subnet-a' },
              securityGroups: [{ id: 'sg-1', name: 'sg-web' }],
            }),
          },
        }}
        onSubmit={() => undefined}
      >
        <NetworkFields
          networkAttachmentName="spec.networkAttachment"
          externalIpAttachmentName="spec.autoExternalIpAttachment"
        />
      </Formik>,
      {
        apiFixtures: {
          virtualNetworks: [
            create(VirtualNetworkSchema, {
              id: 'vn-1',
              metadata: { name: 'vn-prod' },
              spec: { ipv4Cidr: '10.0.0.0/16' },
              status: { state: VirtualNetworkState.READY },
            }),
            create(VirtualNetworkSchema, {
              id: 'vn-2',
              metadata: { name: 'vn-dev' },
              spec: { ipv4Cidr: '10.1.0.0/16' },
              status: { state: VirtualNetworkState.READY },
            }),
          ],
          subnets: [
            create(SubnetSchema, {
              id: 'subnet-1',
              metadata: { name: 'subnet-a' },
              spec: { virtualNetwork: { id: 'vn-1', name: 'vn-prod' }, ipv4Cidr: '10.0.1.0/24' },
              status: { state: SubnetState.READY },
            }),
          ],
          securityGroups: [
            create(SecurityGroupSchema, {
              id: 'sg-1',
              metadata: { name: 'sg-web' },
              spec: { virtualNetwork: { id: 'vn-1' }, ingress: [], egress: [] },
              status: { state: SecurityGroupState.READY },
            }),
          ],
        },
      },
    );

    await waitFor(() =>
      expect(screen.getByLabelText(/^Virtual network/)).toHaveTextContent('vn-prod'),
    );
    await waitFor(() => expect(screen.getByLabelText(/^Subnet/)).toHaveTextContent('subnet-a'));
  });

  it('shows a load error with retry when catalog subnet lookup fails', async () => {
    renderWithProviders(
      <Formik
        initialValues={{
          ...emptyTestValues(false),
          spec: {
            ...emptyTestValues(false).spec,
            networkAttachment: provisioningFieldValue({
              ...emptyTestValues(false).spec.networkAttachment.value,
              subnet: { id: 'subnet-1', name: 'subnet-a' },
            }),
          },
        }}
        onSubmit={() => undefined}
      >
        <NetworkFields
          networkAttachmentName="spec.networkAttachment"
          externalIpAttachmentName="spec.autoExternalIpAttachment"
        />
      </Formik>,
      {
        transportOverrides: {
          onSubnetGet: () => {
            throw new ConnectError('subnet unavailable', Code.Unavailable);
          },
        },
      },
    );

    expect(await screen.findByText('Could not load subnet')).toBeInTheDocument();
    expect(screen.getByText('subnet unavailable')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument();
  });
});
