import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
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

import { ClusterNetworkingStep } from './ClusterNetworkingStep';
import { createEmptyClusterValues } from './fields';
import {
  expectResourceSelectDisabled,
  getResourceSelectCombobox,
} from '../../../test-utils/resourceSelect';
import { renderWithProviders } from '../../../test-utils/TestProviders';
import { provisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';

const virtualNetworks = [
  create(VirtualNetworkSchema, {
    id: 'vn-default',
    metadata: {
      name: 'tenant-workload',
      labels: { 'osac.openshift.io/default': 'true' },
    },
    status: { state: VirtualNetworkState.READY },
  }),
  create(VirtualNetworkSchema, {
    id: 'vn-isolated',
    metadata: { name: 'isolated' },
    status: { state: VirtualNetworkState.READY },
  }),
];

const subnets = [
  create(SubnetSchema, {
    id: 'subnet-other',
    metadata: { name: 'team-subnet' },
    spec: { virtualNetwork: { id: 'vn-isolated' }, ipv4Cidr: '10.43.0.0/24' },
    status: { state: SubnetState.READY },
  }),
  create(SubnetSchema, {
    id: 'subnet-default',
    metadata: {
      name: 'default-ipv4',
      tenant: 'test-tenant',
      labels: { 'osac.openshift.io/default': 'true' },
    },
    spec: {
      virtualNetwork: { id: 'vn-default', name: 'tenant-workload' },
      ipv4Cidr: '10.42.0.0/24',
    },
    status: { state: SubnetState.READY },
  }),
];

const securityGroups = [
  create(SecurityGroupSchema, {
    id: 'sg-default',
    metadata: {
      name: 'allow-ssh-https',
      tenant: 'test-tenant',
      labels: { 'osac.openshift.io/default': 'true' },
    },
    spec: { virtualNetwork: { id: 'vn-default' } },
    status: { state: SecurityGroupState.READY },
  }),
  create(SecurityGroupSchema, {
    id: 'sg-isolated',
    metadata: { name: 'isolated-group' },
    spec: { virtualNetwork: { id: 'vn-isolated' } },
    status: { state: SecurityGroupState.READY },
  }),
  create(SecurityGroupSchema, {
    id: 'sg-isolated-strict',
    metadata: { name: 'isolated-strict' },
    spec: { virtualNetwork: { id: 'vn-isolated' } },
    status: { state: SecurityGroupState.READY },
  }),
];

const renderStep = (values = createEmptyClusterValues()) =>
  renderWithProviders(
    <Formik initialValues={values} onSubmit={() => undefined}>
      {({ values: formValues }) => (
        <>
          <ClusterNetworkingStep />
          <output aria-label="selected security groups">
            {JSON.stringify(formValues.spec.networkAttachment.value.securityGroups)}
          </output>
        </>
      )}
    </Formik>,
    { apiFixtures: { virtualNetworks, subnets, securityGroups } },
  );

describe('ClusterNetworkingStep', () => {
  it('selects the tenant default and displays its resolved network and subnet', async () => {
    renderStep();

    expect(screen.getByRole('radiogroup', { name: 'Network' })).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: /Tenant default/ })).toBeChecked();
    expect(screen.getByRole('radio', { name: /Custom network/ })).not.toBeChecked();
    expect(await screen.findByText('tenant-workload')).toBeInTheDocument();
    expect(screen.queryByText('Primary tenant compute network')).not.toBeInTheDocument();
    expect(screen.getByText('default-ipv4')).toBeInTheDocument();
    expect(screen.getByText('10.42.0.0/24')).toBeInTheDocument();
    expect(await screen.findByText('allow-ssh-https')).toBeInTheDocument();
    expect(screen.queryByText('SSH + HTTPS ingress')).not.toBeInTheDocument();
    expect(screen.getByText('Security group')).toBeInTheDocument();
    expect(screen.queryByLabelText(/^Select virtual network/)).not.toBeInTheDocument();
  });

  it('shows the tenant default subnet when another project is selected', async () => {
    const values = createEmptyClusterValues();
    values.metadata.project = 'project-a';
    const makeDefaultSubnet = (id: string, project: string, cidr: string, seconds: bigint) =>
      create(SubnetSchema, {
        id,
        metadata: {
          name: id,
          tenant: 'test-tenant',
          project,
          labels: { 'osac.openshift.io/default': 'true' },
          creationTimestamp: { seconds, nanos: 0 },
        },
        spec: { virtualNetwork: { id: 'vn-default' }, ipv4Cidr: cidr },
        status: { state: SubnetState.READY },
      });

    renderWithProviders(
      <Formik initialValues={values} onSubmit={() => undefined}>
        <ClusterNetworkingStep />
      </Formik>,
      {
        apiFixtures: {
          virtualNetworks,
          subnets: [
            makeDefaultSubnet('tenant-default', '', '10.41.0.0/24', 1n),
            // Newer, so the summary would pick it if it accepted a default-labeled
            // subnet outside the tenant's default project.
            makeDefaultSubnet('project-a-labeled', 'project-a', '10.40.0.0/24', 2n),
          ],
          securityGroups: [
            create(SecurityGroupSchema, {
              id: 'sg-tenant-default',
              metadata: {
                name: 'tenant-default-group',
                tenant: 'test-tenant',
                project: '',
                labels: { 'osac.openshift.io/default': 'true' },
              },
              spec: { virtualNetwork: { id: 'vn-default' } },
              status: { state: SecurityGroupState.READY },
            }),
          ],
        },
      },
    );

    expect(await screen.findByText('tenant-default')).toBeInTheDocument();
    expect(await screen.findByText('tenant-default-group')).toBeInTheDocument();
    expect(screen.queryByText('project-a-labeled')).not.toBeInTheDocument();
  });

  it('summarizes the newest ready tenant default subnet', async () => {
    const defaultMetadata = {
      tenant: 'test-tenant',
      labels: { 'osac.openshift.io/default': 'true' },
    };
    const makeSubnet = (id: string, seconds: bigint) =>
      create(SubnetSchema, {
        id,
        metadata: {
          ...defaultMetadata,
          name: id,
          creationTimestamp: { seconds, nanos: 0 },
        },
        spec: { virtualNetwork: { id: 'vn-default' }, ipv4Cidr: '10.42.0.0/24' },
        status: { state: SubnetState.READY },
      });
    const makeSecurityGroup = (id: string, seconds: bigint) =>
      create(SecurityGroupSchema, {
        id,
        metadata: {
          ...defaultMetadata,
          name: id,
          creationTimestamp: { seconds, nanos: 0 },
        },
        spec: { virtualNetwork: { id: 'vn-default' } },
        status: { state: SecurityGroupState.READY },
      });

    renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterNetworkingStep />
      </Formik>,
      {
        apiFixtures: {
          virtualNetworks,
          subnets: [makeSubnet('old-subnet', 1n), makeSubnet('new-subnet', 2n)],
          securityGroups: [makeSecurityGroup('old-group', 1n), makeSecurityGroup('new-group', 2n)],
        },
      },
    );

    expect(await screen.findByText('new-subnet')).toBeInTheDocument();
    expect(await screen.findByText('new-group')).toBeInTheDocument();
    expect(screen.queryByText('old-subnet')).not.toBeInTheDocument();
    expect(screen.queryByText('old-group')).not.toBeInTheDocument();
  });

  it('shows cascading custom-network pickers with subnet CIDRs', async () => {
    const { user } = renderStep();

    await user.click(screen.getByRole('radio', { name: /Custom network/ }));
    expect(screen.getByRole('radio', { name: /Custom network/ })).toBeChecked();
    expect(screen.queryByText('default-ipv4')).not.toBeInTheDocument();
    expect(
      screen.getByText('Choose a virtual network, subnet, and security groups.'),
    ).toBeInTheDocument();

    const virtualNetwork = screen.getByLabelText(/^Virtual network/);
    await user.click(virtualNetwork);
    await user.click(await screen.findByRole('option', { name: 'isolated' }));

    const subnet = screen.getByLabelText(/^Subnet/);
    await waitFor(() => expect(subnet).not.toBeDisabled());
    await user.click(subnet);
    expect(
      await screen.findByRole('option', { name: 'team-subnet · 10.43.0.0/24' }),
    ).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: /default-ipv4/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole('option', { name: 'team-subnet · 10.43.0.0/24' }));
    await user.click(screen.getByLabelText(/^Security groups/));
    expect(await screen.findByRole('option', { name: 'isolated-group' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'isolated-strict' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'allow-ssh-https' })).not.toBeInTheDocument();
    await user.click(screen.getByRole('option', { name: 'isolated-group' }));
    await user.click(screen.getByRole('option', { name: 'isolated-strict' }));
    expect(screen.getByLabelText('selected security groups')).toHaveTextContent(
      '[{"id":"sg-isolated","name":"isolated-group"},{"id":"sg-isolated-strict","name":"isolated-strict"}]',
    );
  });

  it('clears custom selections when returning to tenant defaults', async () => {
    const { user } = renderStep();

    await user.click(screen.getByRole('radio', { name: /Custom network/ }));
    await user.click(screen.getByLabelText(/^Virtual network/));
    await user.click(await screen.findByRole('option', { name: 'isolated' }));
    await waitFor(() => expect(screen.getByLabelText(/^Subnet/)).not.toBeDisabled());
    await user.click(screen.getByLabelText(/^Subnet/));
    await user.click(await screen.findByRole('option', { name: 'team-subnet · 10.43.0.0/24' }));
    await user.click(screen.getByLabelText(/^Security groups/));
    await user.click(await screen.findByRole('option', { name: 'isolated-group' }));

    await user.click(screen.getByRole('radio', { name: /Tenant default/ }));
    await user.click(screen.getByRole('radio', { name: /Custom network/ }));

    expect(getResourceSelectCombobox(/^Virtual network/)).toHaveValue('');
    expectResourceSelectDisabled(/^Subnet/);
    expect(screen.getByLabelText('selected security groups')).toHaveTextContent('[]');
  });

  it('keeps automatic external IP attachment off until selected', async () => {
    const { user } = renderStep();
    const autoExternalIp = screen.getByRole('switch', { name: 'Auto-attach external IP' });

    expect(autoExternalIp).not.toBeChecked();
    expect(
      screen.getByText('No external IP at launch. You can attach one later from the instance.'),
    ).toBeInTheDocument();

    await user.click(autoExternalIp);

    expect(autoExternalIp).toBeChecked();
    expect(
      screen.getByText(
        'Attaches an external IP so the instance is reachable outside the private network.',
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: /Tenant default/ })).toBeChecked();
  });

  it('starts with cluster CIDRs collapsed and expands them on request', async () => {
    const { user } = renderStep();
    const expand = screen.getByRole('button', { name: 'Customize cluster network' });

    expect(expand).toHaveAttribute('aria-expanded', 'false');
    await user.click(expand);

    expect(screen.getByRole('button', { name: 'Hide cluster network' })).toHaveAttribute(
      'aria-expanded',
      'true',
    );
    const podCidr = screen.getByRole('textbox', { name: /Pod CIDR/ });
    await user.type(podCidr, '10.128.0.0/14');
    expect(podCidr).toHaveValue('10.128.0.0/14');
    expect(screen.getByRole('textbox', { name: /Service CIDR/ })).toBeInTheDocument();
  });

  it('opens virtual network creation from the picker without leaving the wizard', async () => {
    const { user } = renderStep();

    await user.click(screen.getByRole('radio', { name: /Custom network/ }));
    await user.click(screen.getByLabelText(/^Virtual network/));
    await user.click(await screen.findByRole('option', { name: 'Create virtual network' }));

    expect(screen.getByRole('dialog', { name: 'Create virtual network' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
    expect(screen.getByRole('radio', { name: /Custom network/ })).toBeChecked();
  });

  it('warns when no tenant default network is available', async () => {
    renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterNetworkingStep />
      </Formik>,
    );

    expect(await screen.findByText('Tenant default network is unavailable')).toBeInTheDocument();
  });

  it('honors locked catalog networking fields', () => {
    const values = createEmptyClusterValues();
    values.spec.networkAttachment = provisioningFieldValue(
      values.spec.networkAttachment.value,
      true,
    );
    values.spec.autoExternalIpAttachment = provisioningFieldValue(true, true);
    renderStep(values);

    expect(screen.getByRole('radio', { name: /Tenant default/ })).toBeDisabled();
    expect(screen.getByRole('radio', { name: /Custom network/ })).toBeDisabled();
    expect(screen.getByRole('switch', { name: 'Auto-attach external IP' })).toBeDisabled();
    expect(screen.getByRole('switch', { name: 'Auto-attach external IP' })).toBeChecked();
  });
});
