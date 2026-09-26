import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import type { ClusterCatalogItem } from '@osac/types';

import { ClusterNetworkingStep } from './ClusterNetworkingStep';
import { createEmptyClusterValues } from './payload';
import { renderWithProviders } from '../../../../../test-utils/TestProviders';

const clusterCatalogItem: ClusterCatalogItem = {
  $typeName: 'osac.public.v1.ClusterCatalogItem',
  id: 'catalog-openshift-4',
  metadata: {
    $typeName: 'osac.public.v1.Metadata',
    displayName: '',
    description: '',
    name: 'catalog-openshift-4',
    annotations: {},
    creator: 'foo',
    labels: {},
    project: 'foo',
    tenant: 'foo',
    version: 1,
  },
  title: 'OpenShift 4 cluster',
  description: 'Standard OpenShift cluster offering',
  template: {
    $typeName: 'osac.public.v1.ClusterTemplateReference',
    id: 'tpl-openshift-4',
    name: '',
    project: '',
    shared: false,
  },
  published: true,
  templateParameters: {},
};

describe('ClusterNetworkingStep', () => {
  it('renders Infrastructure Networking and Cluster Networking sections', () => {
    renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterNetworkingStep catalogItem={clusterCatalogItem} />
      </Formik>,
    );

    expect(screen.getByText('Infrastructure Networking')).toBeInTheDocument();
    expect(screen.getByText('Cluster Networking')).toBeInTheDocument();
  });

  it('hides pickers when default network toggle is ON (default)', () => {
    renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterNetworkingStep catalogItem={clusterCatalogItem} />
      </Formik>,
    );

    expect(screen.getByRole('switch', { name: 'Use tenant default network' })).toBeChecked();
    expect(screen.queryByLabelText('Virtual network')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Subnet')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Security groups')).not.toBeInTheDocument();
  });

  it('shows pickers when default network toggle is OFF', async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterNetworkingStep catalogItem={clusterCatalogItem} />
      </Formik>,
    );

    await user.click(screen.getByRole('switch', { name: 'Use tenant default network' }));

    expect(screen.getByText('Virtual network')).toBeInTheDocument();
    expect(screen.getByText('Subnet')).toBeInTheDocument();
    expect(screen.getByText('Security groups')).toBeInTheDocument();
  });

  it('hides pickers when default network toggle is toggled back ON', async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterNetworkingStep catalogItem={clusterCatalogItem} />
      </Formik>,
    );

    // Toggle OFF
    await user.click(screen.getByRole('switch', { name: 'Use tenant default network' }));
    expect(screen.getByText('Virtual network')).toBeInTheDocument();

    // Toggle back ON
    await user.click(screen.getByRole('switch', { name: 'Use tenant default network' }));
    expect(screen.queryByLabelText('Virtual network')).not.toBeInTheDocument();
  });

  it('renders Auto External IP toggle independently', () => {
    renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterNetworkingStep catalogItem={clusterCatalogItem} />
      </Formik>,
    );

    const autoExternalIpSwitch = screen.getByRole('switch', {
      name: 'Auto External IP Attachment',
    });
    expect(autoExternalIpSwitch).not.toBeChecked();
    expect(
      screen.getByText(
        'Automatically provision external IPs for the cluster API and ingress endpoints.',
      ),
    ).toBeInTheDocument();
  });

  it('allows toggling Auto External IP independently of default network toggle', async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterNetworkingStep catalogItem={clusterCatalogItem} />
      </Formik>,
    );

    const autoExternalIpSwitch = screen.getByRole('switch', {
      name: 'Auto External IP Attachment',
    });
    await user.click(autoExternalIpSwitch);

    expect(autoExternalIpSwitch).toBeChecked();
    // Default network toggle remains ON
    expect(screen.getByRole('switch', { name: 'Use tenant default network' })).toBeChecked();
  });

  it('renders Pod CIDR and Service CIDR fields in Cluster Networking section', () => {
    renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterNetworkingStep catalogItem={clusterCatalogItem} />
      </Formik>,
    );

    expect(screen.getByLabelText(/Pod CIDR/)).toBeInTheDocument();
    expect(screen.getByLabelText(/Service CIDR/)).toBeInTheDocument();
  });

  it('accepts CIDR input values', async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterNetworkingStep catalogItem={clusterCatalogItem} />
      </Formik>,
    );

    const podCidr = screen.getByLabelText(/Pod CIDR/);
    await user.type(podCidr, '10.128.0.0/14');
    expect(podCidr).toHaveValue('10.128.0.0/14');
  });
});
