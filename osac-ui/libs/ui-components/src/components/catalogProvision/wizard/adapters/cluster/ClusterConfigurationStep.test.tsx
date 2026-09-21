import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import {
  type ClusterCatalogItem,
  type ClusterVersion,
  ClusterVersionSchema,
  ClusterVersionState,
} from '@osac/types';
import { tIdentity } from '@osac/ui-components/test-utils/i18n';

import ClusterConfigurationStep from './ClusterConfigurationStep';
import { createEmptyNodeSetRow } from './fields';
import { createEmptyClusterValues } from './payload';
import { buildClusterStepSchema } from './schemas';
import { renderWithProviders } from '../../../../../test-utils/TestProviders';
import { FieldValidationProvider } from '../../../../Form/FieldValidationContext';

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

const makeClusterVersion = (
  name: string,
  version: string,
  state: ClusterVersionState,
  enabled = true,
): ClusterVersion =>
  create(ClusterVersionSchema, {
    id: name,
    metadata: { name },
    spec: { version, state, enabled },
  });

// Active + deprecated (enabled) are selectable; obsolete and disabled must be excluded by the filter.
const clusterVersions: ClusterVersion[] = [
  makeClusterVersion('4-17-0', '4.17.0', ClusterVersionState.ACTIVE),
  makeClusterVersion('4-16-0', '4.16.0', ClusterVersionState.DEPRECATED),
  makeClusterVersion('4-15-0', '4.15.0', ClusterVersionState.OBSOLETE),
  makeClusterVersion('4-14-0', '4.14.0', ClusterVersionState.ACTIVE, false),
];

describe('ClusterConfigurationStep', () => {
  it('renders a version select rather than a release-image text field', async () => {
    renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterConfigurationStep catalogItem={clusterCatalogItem} />
      </Formik>,
      { apiFixtures: { clusterVersions } },
    );

    await waitFor(() =>
      expect(screen.getByLabelText(/^Version/)).toHaveTextContent('Select a version'),
    );
    expect(screen.queryByRole('textbox', { name: 'Release image' })).not.toBeInTheDocument();
  });

  it('offers only active and deprecated enabled versions', async () => {
    const { user } = renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterConfigurationStep catalogItem={clusterCatalogItem} />
      </Formik>,
      { apiFixtures: { clusterVersions } },
    );

    await user.click(await screen.findByLabelText(/^Version/));

    expect(screen.getByRole('option', { name: '4.17.0' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: '4.16.0 (deprecated)' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: /4\.15\.0/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('option', { name: /4\.14\.0/ })).not.toBeInTheDocument();
  });

  it('warns when a deprecated version is selected without blocking selection', async () => {
    const { user } = renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterConfigurationStep catalogItem={clusterCatalogItem} />
      </Formik>,
      { apiFixtures: { clusterVersions } },
    );

    await user.click(await screen.findByLabelText(/^Version/));
    await user.click(screen.getByRole('option', { name: '4.16.0 (deprecated)' }));

    await waitFor(() =>
      expect(
        screen.getByText('This version is deprecated and may be removed in a future release.'),
      ).toBeInTheDocument(),
    );
    // The deprecated version stays selected — it is a warning, not a validation error.
    expect(screen.getByLabelText(/^Version/)).toHaveTextContent('4.16.0 (deprecated)');
  });

  it('surfaces a load error with a retry action when versions fail to load', async () => {
    renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterConfigurationStep catalogItem={clusterCatalogItem} />
      </Formik>,
      {
        transportOverrides: {
          onClusterVersionList: () => {
            throw new ConnectError('versions unavailable', Code.Unavailable);
          },
        },
      },
    );

    expect(await screen.findByText('Could not load cluster versions')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument();
  });

  it('starts with one node set and add action', async () => {
    renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterConfigurationStep catalogItem={clusterCatalogItem} />
      </Formik>,
      { apiFixtures: { clusterVersions } },
    );

    await waitFor(() => {
      expect(screen.getByText('Node set 1')).toBeInTheDocument();
      expect(screen.getByText('Select host type')).toBeInTheDocument();
      expect(screen.getByRole('spinbutton', { name: /^Nodes/ })).toBeInTheDocument();
    });
    expect(screen.queryByRole('button', { name: 'Remove node set' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add node set' })).toBeInTheDocument();
  });

  it('adds another node set row when Add node set is clicked', async () => {
    const { user } = renderWithProviders(
      <Formik initialValues={createEmptyClusterValues()} onSubmit={() => undefined}>
        <ClusterConfigurationStep catalogItem={clusterCatalogItem} />
      </Formik>,
      { apiFixtures: { clusterVersions } },
    );

    await user.click(screen.getByRole('button', { name: 'Add node set' }));

    await waitFor(() => {
      expect(screen.getByText('Node set 2')).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Remove node set' })).toBeInTheDocument();
    });
  });

  it('shows pool size validation error when size is zero', async () => {
    const row = createEmptyNodeSetRow();
    renderWithProviders(
      <FieldValidationProvider showErrors>
        <Formik
          initialValues={{
            ...createEmptyClusterValues(),
            catalogItemId: clusterCatalogItem.id,
            spec: {
              ...createEmptyClusterValues().spec,
              versionName: '4-17-0',
              nodeSetRows: [
                {
                  ...row,
                  hostType: 'acme_1tb',
                  size: '3',
                },
              ],
            },
          }}
          validationSchema={buildClusterStepSchema(clusterCatalogItem, 'configuration', tIdentity)}
          validateOnBlur
          onSubmit={() => undefined}
        >
          <ClusterConfigurationStep catalogItem={clusterCatalogItem} />
        </Formik>
      </FieldValidationProvider>,
      { apiFixtures: { clusterVersions } },
    );

    await waitFor(() => {
      expect(screen.getByRole('spinbutton')).toBeInTheDocument();
    });

    const sizeInput = screen.getByRole('spinbutton');
    fireEvent.change(sizeInput, { target: { value: '0' } });
    fireEvent.blur(sizeInput);

    await waitFor(() => {
      expect(screen.getByText('Pool size must be greater than zero')).toBeInTheDocument();
    });
  });

  it('shows duplicate node set name validation on the offending field', async () => {
    const emptyValues = createEmptyClusterValues();
    renderWithProviders(
      <FieldValidationProvider showErrors>
        <Formik
          initialValues={{
            ...emptyValues,
            catalogItemId: clusterCatalogItem.id,
            spec: {
              ...emptyValues.spec,
              versionName: '4-17-0',
              nodeSetRows: [
                { ...createEmptyNodeSetRow(), name: 'workers', hostType: 'acme_1tb', size: '3' },
                { ...createEmptyNodeSetRow(), name: 'workers', hostType: 'acme_1tb', size: '2' },
              ],
            },
          }}
          validationSchema={buildClusterStepSchema(clusterCatalogItem, 'configuration', tIdentity)}
          validateOnMount
          onSubmit={() => undefined}
        >
          <ClusterConfigurationStep catalogItem={clusterCatalogItem} />
        </Formik>
      </FieldValidationProvider>,
      { apiFixtures: { clusterVersions } },
    );

    await waitFor(() => {
      expect(screen.getByText('Node set names must be unique')).toBeInTheDocument();
    });
    expect(screen.getAllByLabelText(/^Name/)[1]).toHaveAttribute('aria-invalid', 'true');
  });
});
