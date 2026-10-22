import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { SecretSchema, SecretType } from '@osac/types';

import SshPublicKeySecretField from './SshPublicKeySecretField';
import { renderWithProviders } from '../../../../test-utils/TestProviders';

const secrets = [
  create(SecretSchema, {
    id: 'ssh-1',
    metadata: { name: 'tenant-ssh-key', project: 'project-a' },
    type: SecretType.SSH_PUBLIC_KEY,
  }),
];

describe('SshPublicKeySecretField', () => {
  it('lets the user select an SSH public key secret', async () => {
    const { user } = renderWithProviders(
      <Formik initialValues={{ spec: { sshKey: '' } }} onSubmit={() => undefined}>
        {({ values }) => (
          <>
            <SshPublicKeySecretField name="spec.sshKey" project="project-a" />
            <output aria-label="selected-secret">{values.spec.sshKey}</output>
          </>
        )}
      </Formik>,
      { apiFixtures: { secrets } },
    );

    await user.click(await screen.findByLabelText(/^SSH public key/));
    await user.click(await screen.findByRole('option', { name: 'tenant-ssh-key' }));

    await waitFor(() => {
      expect(screen.getByLabelText('selected-secret')).toHaveTextContent('tenant-ssh-key');
    });
  });

  it('disables the control when requested', async () => {
    renderWithProviders(
      <Formik initialValues={{ spec: { sshKey: 'tenant-ssh-key' } }} onSubmit={() => undefined}>
        <SshPublicKeySecretField name="spec.sshKey" project="project-a" isDisabled />
      </Formik>,
      { apiFixtures: { secrets } },
    );

    await waitFor(() => {
      expect(screen.getByLabelText(/^SSH public key/)).toBeDisabled();
    });
  });
});
