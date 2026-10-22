import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { type ClusterVersion, ClusterVersionSchema, ClusterVersionState } from '@osac/types';

import ClusterVersionField from './ClusterVersionField';
import { renderWithProviders } from '../../../../test-utils/TestProviders';

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

const clusterVersions: ClusterVersion[] = [
  makeClusterVersion('4-17-0', '4.17.0', ClusterVersionState.ACTIVE),
  makeClusterVersion('4-16-0', '4.16.0', ClusterVersionState.DEPRECATED),
  makeClusterVersion('4-15-0', '4.15.0', ClusterVersionState.OBSOLETE),
];

const renderField = (initialValue = '', options?: Parameters<typeof renderWithProviders>[1]) =>
  renderWithProviders(
    <Formik initialValues={{ spec: { versionName: initialValue } }} onSubmit={() => undefined}>
      <ClusterVersionField name="spec.versionName" />
    </Formik>,
    options,
  );

describe('ClusterVersionField', () => {
  it('offers only active and deprecated enabled versions', async () => {
    const { user } = renderField('', { apiFixtures: { clusterVersions } });

    await user.click(await screen.findByLabelText(/^Version/));

    expect(screen.getByRole('option', { name: '4.17.0' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: '4.16.0 (deprecated)' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: /4\.15\.0/ })).not.toBeInTheDocument();
  });

  it('warns when a deprecated version is selected', async () => {
    const { user } = renderField('', { apiFixtures: { clusterVersions } });

    await user.click(await screen.findByLabelText(/^Version/));
    await user.click(screen.getByRole('option', { name: '4.16.0 (deprecated)' }));

    await waitFor(() => {
      expect(
        screen.getByText('This version is deprecated and may be removed in a future release.'),
      ).toBeInTheDocument();
    });
  });

  it('surfaces a load error with retry', async () => {
    renderField('', {
      transportOverrides: {
        onClusterVersionList: () => {
          throw new ConnectError('versions unavailable', Code.Unavailable);
        },
      },
    });

    expect(await screen.findByText('Could not load cluster versions')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument();
  });
});
