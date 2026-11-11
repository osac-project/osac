import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { type ClusterVersion, ClusterVersionSchema, ClusterVersionState } from '@osac/types';

import { ClusterReviewStep } from './ClusterReviewStep';
import { createEmptyClusterValues } from './fields';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const clusterVersions: ClusterVersion[] = [
  create(ClusterVersionSchema, {
    id: '4-17-0',
    metadata: { name: '4-17-0' },
    spec: { version: '4.17.0', state: ClusterVersionState.ACTIVE, enabled: true },
  }),
];

describe('ClusterReviewStep', () => {
  it('renders Infrastructure Networking and Cluster Networking section headings', async () => {
    const emptyValues = createEmptyClusterValues();
    renderWithProviders(
      <Formik initialValues={emptyValues} onSubmit={() => undefined}>
        <ClusterReviewStep />
      </Formik>,
    );

    expect(
      await screen.findByRole('heading', { name: 'Infrastructure Networking', level: 3 }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('heading', { name: 'Cluster Networking', level: 3 }),
    ).toBeInTheDocument();
  });

  it('shows selected security groups for a custom network', async () => {
    const values = createEmptyClusterValues();
    values.spec.networkAttachment.value = {
      useDefaultNetwork: false,
      virtualNetwork: { id: 'vn-1', name: 'team-network' },
      subnet: { id: 'subnet-1', name: 'team-subnet' },
      securityGroups: [
        { id: 'sg-1', name: 'web' },
        { id: 'sg-2', name: 'internal' },
      ],
    };
    renderWithProviders(
      <Formik initialValues={values} onSubmit={() => undefined}>
        <ClusterReviewStep />
      </Formik>,
    );

    expect(await screen.findByText('web, internal')).toBeInTheDocument();
  });

  it('shows the human-readable version, not the metadata.name slug', async () => {
    const emptyValues = createEmptyClusterValues();
    renderWithProviders(
      <Formik
        initialValues={{
          ...emptyValues,
          spec: { ...emptyValues.spec, versionName: { value: '4-17-0', locked: false } },
        }}
        onSubmit={() => undefined}
      >
        <ClusterReviewStep />
      </Formik>,
      { apiFixtures: { clusterVersions } },
    );

    expect(await screen.findByText('4.17.0')).toBeInTheDocument();
    expect(screen.queryByText('4-17-0')).not.toBeInTheDocument();
  });

  it('uses the simple host type and size display for a node set', async () => {
    const values = createEmptyClusterValues();
    values.spec.nodeSetRows.value = [
      {
        rowId: 'workers-row',
        name: 'workers',
        hostType: { id: 'bmit-worker', name: 'worker' },
        size: 1,
      },
    ];
    renderWithProviders(
      <Formik initialValues={values} onSubmit={() => undefined}>
        <ClusterReviewStep />
      </Formik>,
    );

    expect(await screen.findByText('worker: 1')).toBeInTheDocument();
  });
});
