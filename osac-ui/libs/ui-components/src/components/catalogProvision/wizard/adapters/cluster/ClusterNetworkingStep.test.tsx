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
  it('renders optional pod and service CIDR fields', () => {
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
