import { createRouterTransport } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { type Secret, Secrets } from '@osac/types';
import { wrapWithAuthInterceptor } from '@osac/ui-components/test-utils/createMockConnectTransport';

import SecretCreatePage from './SecretCreateWizard';
import { renderWithProviders } from '../../../test-utils/TestProviders';

describe('SecretCreatePage', () => {
  it('creates an admin secret in the root project without asking for a project', async () => {
    let createdSecret: Secret | undefined;
    const transport = wrapWithAuthInterceptor(
      createRouterTransport((router) => {
        router.service(Secrets, {
          create: ({ object }) => {
            createdSecret = object;
            return { object };
          },
        });
      }),
    );
    const { user } = renderWithProviders(<SecretCreatePage />, {
      session: { role: 'admin' },
      transport,
    });

    expect(screen.queryByRole('button', { name: 'Project' })).not.toBeInTheDocument();
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'admin-secret');
    await user.click(screen.getByRole('button', { name: 'Next' }));

    await user.type(screen.getByRole('textbox', { name: 'Key' }), 'token');
    await user.type(screen.getByRole('textbox', { name: 'Value' }), 'secret-value');
    await user.click(screen.getByRole('button', { name: 'Next' }));

    expect(await screen.findByRole('heading', { name: 'Review' })).toBeInTheDocument();
    expect(screen.getByText('Default')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => {
      expect(createdSecret?.metadata).toMatchObject({
        name: 'admin-secret',
        project: '',
      });
    });
  });
});
