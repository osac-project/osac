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
  metadata: { project: string };
  spec: {
    networkAttachment: ProvisioningFieldValue<NetworkAttachmentFormFields>;
    autoExternalIpAttachment: ProvisioningFieldValue<boolean>;
  };
}

const emptyTestValues = (useDefaultNetwork = true): TestFormValues => ({
  metadata: { project: '' },
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
  it('renders the shared network cards and external IP switch', () => {
    renderFields();

    expect(screen.getByRole('radiogroup', { name: 'Network' })).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: /Tenant default/ })).toBeChecked();
    expect(screen.getByRole('radio', { name: /Custom network/ })).not.toBeChecked();
    expect(screen.getByRole('switch', { name: 'Auto-attach external IP' })).not.toBeChecked();
  });

  it('hides VN, Subnet, and Security Groups pickers when tenant default is selected', () => {
    renderFields();

    expect(screen.getByRole('radio', { name: /Tenant default/ })).toBeChecked();
    expect(screen.queryByText('Virtual network')).not.toBeInTheDocument();
    expect(screen.queryByText('Subnet')).not.toBeInTheDocument();
    expect(screen.queryByText('Security groups')).not.toBeInTheDocument();
  });

  it('shows VN, Subnet, and Security Groups pickers when custom network is selected', async () => {
    const user = userEvent.setup();
    renderFields();

    await user.click(screen.getByRole('radio', { name: /Custom network/ }));

    expect(screen.getByText('Virtual network')).toBeInTheDocument();
    expect(screen.getByText('Subnet')).toBeInTheDocument();
    expect(screen.getByText('Security groups')).toBeInTheDocument();
  });

  it('hides pickers when switching back to tenant default', async () => {
    const user = userEvent.setup();
    renderFields();

    await user.click(screen.getByRole('radio', { name: /Custom network/ }));
    expect(screen.getByText('Virtual network')).toBeInTheDocument();

    await user.click(screen.getByRole('radio', { name: /Tenant default/ }));
    expect(screen.queryByText('Virtual network')).not.toBeInTheDocument();
  });

  it('renders the workload-specific external IP helper when enabled', async () => {
    const { user } = renderWithProviders(
      <Formik initialValues={emptyTestValues()} onSubmit={() => undefined}>
        <NetworkFields
          networkAttachmentName="spec.networkAttachment"
          externalIpAttachmentName="spec.autoExternalIpAttachment"
          autoExternalIpHelperText="Provision external IPs automatically."
        />
      </Formik>,
    );

    await user.click(screen.getByRole('switch', { name: 'Auto-attach external IP' }));
    expect(screen.getByText('Provision external IPs automatically.')).toBeInTheDocument();
  });

  it('does not display validation errors on initial render', () => {
    renderFields(emptyTestValues(false));

    expect(screen.queryByText('Virtual network is required')).not.toBeInTheDocument();
    expect(screen.queryByText('Subnet is required')).not.toBeInTheDocument();
  });

  it('disables the network cards and pickers together', () => {
    renderFields(emptyTestValues(false), true);

    expect(screen.getByRole('radio', { name: /Tenant default/ })).toBeDisabled();
    expect(screen.getByRole('radio', { name: /Custom network/ })).toBeDisabled();
    expect(screen.getByLabelText(/^Virtual network/)).toBeDisabled();
    expect(screen.getByLabelText(/^Subnet/)).toBeDisabled();
    expect(screen.getByLabelText(/^Security groups/).closest('.pf-v6-c-menu-toggle')).toHaveClass(
      'pf-m-disabled',
    );
    expect(screen.getByRole('switch', { name: 'Auto-attach external IP' })).toBeDisabled();
  });

  it('disables auto external IP from its own catalog policy', () => {
    renderFields({
      ...emptyTestValues(),
      spec: {
        ...emptyTestValues().spec,
        autoExternalIpAttachment: provisioningFieldValue(false, true),
      },
    });

    expect(screen.getByRole('radio', { name: /Tenant default/ })).not.toBeDisabled();
    expect(screen.getByRole('switch', { name: 'Auto-attach external IP' })).toBeDisabled();
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
