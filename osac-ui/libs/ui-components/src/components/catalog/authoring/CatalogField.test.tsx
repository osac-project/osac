import { screen } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import CatalogField from './CatalogField';
import { renderWithProviders } from '../../../test-utils/TestProviders';
import { InputField } from '../../Form/InputField';
import type { ProvisioningFieldValue } from '../../ProvisioningWizard/fields/provisioningFieldValue';

interface Values {
  userData: ProvisioningFieldValue<string>;
}

const renderCatalogField = (initial: ProvisioningFieldValue<string>) =>
  renderWithProviders(
    <Formik<Values> initialValues={{ userData: initial }} onSubmit={() => {}}>
      <CatalogField name="userData">
        {({ valueName, isDisabled, labelInfo }) => (
          <InputField
            name={valueName}
            label="User data"
            fieldId="user-data"
            isDisabled={isDisabled}
            labelInfo={labelInfo}
          />
        )}
      </CatalogField>
    </Formik>,
  );

describe('CatalogField', () => {
  it('renders checked and labeled Editable when the field starts unlocked', () => {
    renderCatalogField({ value: '', locked: false });

    const toggle = screen.getByRole('switch');
    expect(toggle).toBeChecked();
    expect(toggle).toHaveAccessibleName('Editable');
  });

  it('renders unchecked and labeled Locked when the field starts locked', () => {
    renderCatalogField({ value: 'preset', locked: true });

    const toggle = screen.getByRole('switch');
    expect(toggle).not.toBeChecked();
    expect(toggle).toHaveAccessibleName('Locked');
  });

  it('keeps the wrapped control enabled while locked', () => {
    renderCatalogField({ value: 'preset', locked: true });

    expect(screen.getByRole('textbox', { name: 'User data' })).toBeEnabled();
  });

  it('switches the field to locked without changing its value', async () => {
    const { user } = renderCatalogField({ value: 'cloud-init', locked: false });

    await user.click(screen.getByRole('switch'));

    expect(screen.getByRole('switch')).not.toBeChecked();
    expect(screen.getByRole('textbox', { name: 'User data' })).toHaveValue('cloud-init');
  });

  it('switches the field to editable without changing its value', async () => {
    const { user } = renderCatalogField({ value: 'cloud-init', locked: true });

    await user.click(screen.getByRole('switch'));

    expect(screen.getByRole('switch')).toBeChecked();
    expect(screen.getByRole('textbox', { name: 'User data' })).toHaveValue('cloud-init');
  });

  it('typing a value does not change the locked state', async () => {
    const { user } = renderCatalogField({ value: '', locked: true });

    await user.type(screen.getByRole('textbox', { name: 'User data' }), 'script');

    expect(screen.getByRole('switch')).not.toBeChecked();
  });
});
