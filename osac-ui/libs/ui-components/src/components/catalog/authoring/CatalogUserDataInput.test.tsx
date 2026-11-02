import { screen } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { catalogUserDataValuesFromPolicy } from './catalogUserData';
import CatalogUserDataInput from './CatalogUserDataInput';
import { renderWithProviders } from '../../../test-utils/TestProviders';
import type { ProvisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';

interface Values {
  userData: ProvisioningFieldValue<string>;
}

describe('CatalogUserDataInput', () => {
  it('shows an empty text input when user data references a secret', () => {
    const initialValues = catalogUserDataValuesFromPolicy({
      behavior: {
        case: 'editable',
        value: { defaultValue: { source: { case: 'secret', value: { name: 'my-secret' } } } },
      },
    } as never);

    renderWithProviders(
      <Formik<Values> initialValues={initialValues} onSubmit={() => undefined}>
        <CatalogUserDataInput
          valueName="userData.value"
          isDisabled={false}
          isRequired={false}
          labelInfo={undefined}
        />
      </Formik>,
    );

    const input = screen.getByRole('textbox', { name: 'User data' });
    expect(input).toHaveValue('');
    expect(input).toBeEnabled();
  });
});
