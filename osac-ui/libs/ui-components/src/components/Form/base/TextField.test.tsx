import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Formik, useFormikContext } from 'formik';
import { describe, expect, it } from 'vitest';

import TextField from './TextField';

const FormikFieldObserver = ({ name, label }: { name: string; label: string }) => {
  const { values } = useFormikContext<Record<string, string>>();
  return <span aria-label={label}>{values[name] ?? ''}</span>;
};

describe('TextField', () => {
  it('stores trimmed text while preserving the draft until blur', async () => {
    const user = userEvent.setup();

    render(
      <Formik initialValues={{ name: '' }} onSubmit={() => undefined}>
        <>
          <TextField name="name" label="Name" fieldId="name" />
          <FormikFieldObserver name="name" label="Formik name" />
        </>
      </Formik>,
    );

    const input = screen.getByRole('textbox', { name: 'Name' });
    await user.type(input, '  ');
    expect(input).toHaveValue('  ');
    expect(screen.getByLabelText('Formik name').textContent).toBe('');

    await user.type(input, 'production cluster  ');
    expect(input).toHaveValue('  production cluster  ');
    expect(screen.getByLabelText('Formik name').textContent).toBe('production cluster');

    await user.tab();
    expect(input).toHaveValue('production cluster');
  });

  it('preserves an untouched prefilled text value on blur', async () => {
    const user = userEvent.setup();

    render(
      <Formik initialValues={{ name: '  prod-v4  ' }} onSubmit={() => undefined}>
        <>
          <TextField name="name" label="Name" fieldId="name" />
          <FormikFieldObserver name="name" label="Formik name" />
        </>
      </Formik>,
    );

    const input = screen.getByRole('textbox', { name: 'Name' });
    await user.click(input);
    await user.tab();

    expect(input).toHaveValue('  prod-v4  ');
    expect(screen.getByLabelText('Formik name').textContent).toBe('  prod-v4  ');
  });

  it('stores trimmed password text while preserving the draft until blur', async () => {
    const user = userEvent.setup();

    render(
      <Formik initialValues={{ password: '' }} onSubmit={() => undefined}>
        <>
          <TextField name="password" label="Password" fieldId="password" type="password" />
          <FormikFieldObserver name="password" label="Formik password" />
        </>
      </Formik>,
    );

    const input = screen.getByLabelText('Password');
    await user.type(input, '  secret phrase  ');
    expect(input).toHaveValue('  secret phrase  ');
    expect(screen.getByLabelText('Formik password').textContent).toBe('secret phrase');

    await user.tab();
    expect(input).toHaveValue('secret phrase');
  });
});
