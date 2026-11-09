import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { SecretSchema, SecretType } from '@osac/types';

import BareMetalAccessStep from './BareMetalAccessStep';
import { createEmptyBareMetalInstanceValues } from './fields';
import { renderWithProviders } from '../../../test-utils/TestProviders';
import { provisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';

describe('BareMetalAccessStep', () => {
  it('lets the user switch between inline user data and a USER_DATA Secret', async () => {
    const values = createEmptyBareMetalInstanceValues();
    values.spec.userData = provisioningFieldValue({
      source: 'inline',
      inline: 'inline data',
      secret: 'stale-secret',
    });

    const { user } = renderWithProviders(
      <Formik initialValues={values} onSubmit={() => undefined}>
        <BareMetalAccessStep />
      </Formik>,
      {
        apiFixtures: {
          secrets: [
            create(SecretSchema, {
              id: 'user-data-secret-id',
              metadata: { name: 'user-data-secret', project: '' },
              data: {},
              type: SecretType.USER_DATA,
            }),
          ],
        },
      },
    );

    await screen.findByRole('radiogroup', { name: 'User data' });
    expect(screen.getByRole('textbox', { name: 'File upload' })).toHaveValue('inline data');

    await user.click(screen.getByRole('radio', { name: 'Select secret' }));

    expect(screen.queryByRole('textbox', { name: 'File upload' })).not.toBeInTheDocument();
    const secretSelect = await screen.findByRole('button', {
      name: 'Select a user data secret',
    });
    await user.click(secretSelect);
    await user.click(screen.getByRole('option', { name: 'user-data-secret' }));

    await user.click(screen.getByRole('radio', { name: 'Enter value' }));

    expect(screen.getByRole('textbox', { name: 'File upload' })).toHaveValue('inline data');
    expect(
      screen.queryByRole('button', { name: 'Select a user data secret' }),
    ).not.toBeInTheDocument();
  });
});
