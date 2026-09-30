import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { type ClusterVersion, ClusterVersionSchema, ClusterVersionState } from '@osac/types';

import { ClusterReviewStep } from './ClusterReviewStep';
import { createEmptyClusterValues } from './payload';
import { renderWithProviders } from '../../../../../test-utils/TestProviders';

const clusterVersions: ClusterVersion[] = [
  create(ClusterVersionSchema, {
    id: '4-17-0',
    metadata: { name: '4-17-0' },
    spec: { version: '4.17.0', state: ClusterVersionState.ACTIVE, enabled: true },
  }),
];

describe('ClusterReviewStep', () => {
  it('shows the human-readable version, not the metadata.name slug', async () => {
    const emptyValues = createEmptyClusterValues();
    renderWithProviders(
      <Formik
        initialValues={{ ...emptyValues, spec: { ...emptyValues.spec, versionName: '4-17-0' } }}
        onSubmit={() => undefined}
      >
        <ClusterReviewStep catalogItem={null} />
      </Formik>,
      { apiFixtures: { clusterVersions } },
    );

    expect(await screen.findByText('4.17.0')).toBeInTheDocument();
    expect(screen.queryByText('4-17-0')).not.toBeInTheDocument();
  });
});
