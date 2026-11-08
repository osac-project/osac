import { screen } from '@testing-library/react';
import { Form, Formik } from 'formik';
import { describe, expect, it, vi } from 'vitest';

import {
  type CatalogUserDataFormValues,
  catalogUserDataPolicyFromValues,
  catalogUserDataValuesFromPolicy,
} from './catalogUserData';
import CatalogUserDataInput from './CatalogUserDataInput';
import { renderWithProviders } from '../../test-utils/TestProviders';

describe('CatalogUserDataInput', () => {
  it('shows an empty text input when user data references a secret', () => {
    const initialValues = catalogUserDataValuesFromPolicy({
      behavior: {
        case: 'editable',
        value: { defaultValue: { source: { case: 'secret', value: { name: 'my-secret' } } } },
      },
    } as never);

    renderWithProviders(
      <Formik<CatalogUserDataFormValues> initialValues={initialValues} onSubmit={() => undefined}>
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

  it('preserves the Secret source when the inline input is only focused', async () => {
    const initialValues = catalogUserDataValuesFromPolicy({
      behavior: {
        case: 'editable',
        value: {
          defaultValue: {
            source: { case: 'secret', value: { id: 'secret-id', name: 'my-secret' } },
          },
        },
      },
    } as never);
    let submittedPolicy: ReturnType<typeof catalogUserDataPolicyFromValues>;
    const onSubmit = vi.fn((values: CatalogUserDataFormValues) => {
      submittedPolicy = catalogUserDataPolicyFromValues(values);
    });
    const { user } = renderWithProviders(
      <Formik<CatalogUserDataFormValues> initialValues={initialValues} onSubmit={onSubmit}>
        <Form>
          <CatalogUserDataInput
            valueName="userData.value"
            isDisabled={false}
            isRequired={false}
            labelInfo={undefined}
          />
          <button type="submit">Save</button>
        </Form>
      </Formik>,
    );

    await user.click(screen.getByRole('textbox', { name: 'User data' }));
    await user.click(screen.getByRole('button', { name: 'Save' }));

    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        userData: { value: '', locked: false },
        userDataSecret: { value: 'my-secret', locked: false },
      }),
      expect.anything(),
    );
    expect(submittedPolicy).toEqual({
      behavior: {
        case: 'editable',
        value: { defaultValue: { source: { case: 'secret', value: { name: 'my-secret' } } } },
      },
    });
  });
});
