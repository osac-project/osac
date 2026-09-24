import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { SwitchField } from './SwitchField';

const renderSwitch = ({
  initialValue = false,
  isDisabled = false,
  helperText,
}: { initialValue?: boolean; isDisabled?: boolean; helperText?: string } = {}) =>
  render(
    <Formik initialValues={{ autoExternalIp: initialValue }} onSubmit={() => undefined}>
      {({ values }) => (
        <>
          <SwitchField
            name="autoExternalIp"
            label="Auto external IP"
            fieldId="auto-external-ip"
            isDisabled={isDisabled}
            helperText={helperText}
          />
          <output aria-label="formik-value">{String(values.autoExternalIp)}</output>
        </>
      )}
    </Formik>,
  );

describe('SwitchField', () => {
  it('renders unchecked when the initial Formik value is false', () => {
    renderSwitch();

    expect(screen.getByRole('switch', { name: 'Auto external IP' })).not.toBeChecked();
  });

  it('renders checked when the initial Formik value is true', () => {
    renderSwitch({ initialValue: true });

    expect(screen.getByRole('switch', { name: 'Auto external IP' })).toBeChecked();
  });

  it('updates the Formik value when toggled on', async () => {
    const user = userEvent.setup();
    renderSwitch();

    await user.click(screen.getByRole('switch', { name: 'Auto external IP' }));

    expect(screen.getByLabelText('formik-value')).toHaveTextContent('true');
  });

  it('updates the Formik value when toggled off', async () => {
    const user = userEvent.setup();
    renderSwitch({ initialValue: true });

    await user.click(screen.getByRole('switch', { name: 'Auto external IP' }));

    expect(screen.getByLabelText('formik-value')).toHaveTextContent('false');
  });

  it('disables the switch when isDisabled is true', () => {
    renderSwitch({ isDisabled: true });

    expect(screen.getByRole('switch', { name: 'Auto external IP' })).toBeDisabled();
  });

  it('renders helper text when provided', () => {
    renderSwitch({ helperText: 'Automatically provision external IPs.' });

    expect(screen.getByText('Automatically provision external IPs.')).toBeInTheDocument();
  });
});
