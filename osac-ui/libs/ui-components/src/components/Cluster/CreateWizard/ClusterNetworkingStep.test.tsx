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
import { renderWithProviders } from '../../../test-utils/TestProviders';
import { provisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';

const virtualNetworks = [
  create(VirtualNetworkSchema, {
    id: 'vn-default',
    metadata: {
      name: 'tenant-workload',
      description: 'Primary tenant compute network',
      labels: { 'osac.openshift.io/default': 'true' },
    },
    status: { state: VirtualNetworkState.READY },
  }),
  create(VirtualNetworkSchema, {
    id: 'vn-isolated',
    metadata: { name: 'isolated', description: 'Private team network' },
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
    metadata: { name: 'default-ipv4', labels: { 'osac.openshift.io/default': 'true' } },
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
      description: 'SSH + HTTPS ingress',
      labels: { 'osac.openshift.io/default': 'true' },
    },
    spec: { virtualNetwork: { id: 'vn-default' } },
    status: { state: SecurityGroupState.READY },
  }),
  create(SecurityGroupSchema, {
    id: 'sg-isolated',
    metadata: { name: 'isolated-group', description: 'Private ingress' },
    spec: { virtualNetwork: { id: 'vn-isolated' } },
    status: { state: SecurityGroupState.READY },
  }),
  create(SecurityGroupSchema, {
    id: 'sg-isolated-strict',
    metadata: { name: 'isolated-strict', description: 'Restricted ingress' },
    spec: { virtualNetwork: { id: 'vn-isolated' } },
    status: { state: SecurityGroupState.READY },
  }),
];

const renderStep = (values = createEmptyClusterValues()) =>
  renderWithProviders(
    <Formik initialValues={values} onSubmit={() => undefined}>
      <ClusterNetworkingStep />
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
    expect(screen.getByText('Primary tenant compute network')).toBeInTheDocument();
    expect(screen.getByText('default-ipv4')).toBeInTheDocument();
    expect(screen.getByText('10.42.0.0/24')).toBeInTheDocument();
    expect(screen.getByText('allow-ssh-https')).toBeInTheDocument();
    expect(screen.getByText('SSH + HTTPS ingress')).toBeInTheDocument();
    expect(screen.queryByLabelText(/^Select virtual network/)).not.toBeInTheDocument();
  });

  it('shows the default subnet for the selected project', async () => {
    const values = createEmptyClusterValues();
    values.metadata.project = 'project-a';
    const otherProjectSubnet = create(SubnetSchema, {
      id: 'subnet-project-b',
      metadata: {
        name: 'project-b-default',
        project: 'project-b',
        labels: { 'osac.openshift.io/default': 'true' },
      },
      spec: { virtualNetwork: { id: 'vn-default' }, ipv4Cidr: '10.40.0.0/24' },
      status: { state: SubnetState.READY },
    });
    const selectedProjectSubnet = create(SubnetSchema, {
      id: 'subnet-project-a',
      metadata: {
        name: 'project-a-default',
        project: 'project-a',
        labels: { 'osac.openshift.io/default': 'true' },
      },
      spec: { virtualNetwork: { id: 'vn-default' }, ipv4Cidr: '10.41.0.0/24' },
      status: { state: SubnetState.READY },
    });

    renderWithProviders(
      <Formik initialValues={values} onSubmit={() => undefined}>
        <ClusterNetworkingStep />
      </Formik>,
      {
        apiFixtures: {
          virtualNetworks,
          subnets: [otherProjectSubnet, selectedProjectSubnet],
          securityGroups: [
            create(SecurityGroupSchema, {
              id: 'sg-project-a',
              metadata: {
                name: 'project-a-group',
                project: 'project-a',
                labels: { 'osac.openshift.io/default': 'true' },
              },
              spec: { virtualNetwork: { id: 'vn-default' } },
              status: { state: SecurityGroupState.READY },
            }),
          ],
        },
      },
    );

    expect(await screen.findByText('project-a-default')).toBeInTheDocument();
    expect(screen.getByText('project-a-group')).toBeInTheDocument();
    expect(screen.queryByText('project-b-default')).not.toBeInTheDocument();
  });

  it('summarizes the newest ready tenant defaults used by the server', async () => {
    const defaultMetadata = { labels: { 'osac.openshift.io/default': 'true' } };
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
    expect(screen.getByText('new-group')).toBeInTheDocument();
    expect(screen.queryByText('old-subnet')).not.toBeInTheDocument();
    expect(screen.queryByText('old-group')).not.toBeInTheDocument();
  });

  it('shows cascading custom-network pickers with descriptions', async () => {
    const { user } = renderStep();

    await user.click(screen.getByRole('radio', { name: /Custom network/ }));
    expect(screen.getByRole('radio', { name: /Custom network/ })).toBeChecked();
    expect(screen.queryByText('default-ipv4')).not.toBeInTheDocument();

    const virtualNetwork = screen.getByLabelText(/^Virtual network/);
    await user.click(virtualNetwork);
    await user.click(
      await screen.findByRole('option', { name: 'isolated · Private team network' }),
    );

    const subnet = screen.getByLabelText(/^Subnet/);
    await waitFor(() => expect(subnet).not.toBeDisabled());
    await user.click(subnet);
    expect(
      await screen.findByRole('option', { name: 'team-subnet · 10.43.0.0/24' }),
    ).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: /default-ipv4/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole('option', { name: 'team-subnet · 10.43.0.0/24' }));

    const securityGroup = screen.getByLabelText(/^Security group/);
    await waitFor(() => expect(securityGroup).not.toBeDisabled());
    await user.click(securityGroup);
    expect(
      screen.getByRole('option', { name: 'isolated-group · Private ingress' }),
    ).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: /allow-ssh-https/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole('option', { name: 'isolated-group · Private ingress' }));
    expect(securityGroup).toHaveTextContent('isolated-group · Private ingress');

    await user.click(securityGroup);
    await user.click(screen.getByRole('option', { name: 'isolated-strict · Restricted ingress' }));
    expect(securityGroup).toHaveTextContent('isolated-strict · Restricted ingress');
    expect(securityGroup).not.toHaveTextContent('isolated-group · Private ingress');
  });

  it('clears custom selections when returning to tenant defaults', async () => {
    const { user } = renderStep();

    await user.click(screen.getByRole('radio', { name: /Custom network/ }));
    await user.click(screen.getByLabelText(/^Virtual network/));
    await user.click(
      await screen.findByRole('option', { name: 'isolated · Private team network' }),
    );
    await waitFor(() => expect(screen.getByLabelText(/^Subnet/)).not.toBeDisabled());
    await user.click(screen.getByLabelText(/^Subnet/));
    await user.click(await screen.findByRole('option', { name: 'team-subnet · 10.43.0.0/24' }));

    await user.click(screen.getByRole('radio', { name: /Tenant default/ }));
    await user.click(screen.getByRole('radio', { name: /Custom network/ }));

    expect(screen.getByLabelText(/^Virtual network/)).toHaveTextContent('Select virtual network');
    expect(screen.getByLabelText(/^Subnet/)).toBeDisabled();
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
