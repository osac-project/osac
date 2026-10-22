import { screen } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import ProvisioningField from './ProvisioningField';
import { provisioningFieldValue } from './provisioningFieldValue';
import { renderWithProviders } from '../../../test-utils/TestProviders';
import { InputField } from '../../Form/InputField';

describe('ProvisioningField', () => {
  it('binds the child to the nested value path and disables it when locked', () => {
    renderWithProviders(
      <Formik
        initialValues={{ spec: { userData: provisioningFieldValue('cloud-init', true) } }}
        onSubmit={() => undefined}
      >
        <ProvisioningField name="spec.userData">
          {({ valueName, isDisabled }) => (
            <InputField
              name={valueName}
              label="User data"
              fieldId="user-data"
              isDisabled={isDisabled}
            />
          )}
        </ProvisioningField>
      </Formik>,
    );

    const input = screen.getByLabelText(/^User data/);
    expect(input).toHaveValue('cloud-init');
    expect(input).toBeDisabled();
  });

  it('leaves the child editable when locked is false', () => {
    renderWithProviders(
      <Formik
        initialValues={{ spec: { userData: provisioningFieldValue('cloud-init') } }}
        onSubmit={() => undefined}
      >
        <ProvisioningField name="spec.userData">
          {({ valueName, isDisabled }) => (
            <InputField
              name={valueName}
              label="User data"
              fieldId="user-data"
              isDisabled={isDisabled}
            />
          )}
        </ProvisioningField>
      </Formik>,
    );

    expect(screen.getByLabelText(/^User data/)).not.toBeDisabled();
  });
});
