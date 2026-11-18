import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Formik, useFormikContext } from 'formik';
import { describe, expect, it } from 'vitest';

import TextAreaField from './TextAreaField';

const FormikFieldObserver = ({ name, label }: { name: string; label: string }) => {
  const { values } = useFormikContext<Record<string, string>>();
  return <span aria-label={label}>{values[name] ?? ''}</span>;
};

describe('TextAreaField', () => {
  it('leaves an untouched optional value undefined on blur', async () => {
    const user = userEvent.setup();

    render(
      <Formik initialValues={{ notes: undefined as string | undefined }} onSubmit={() => undefined}>
        {({ values, dirty }) => (
          <>
            <TextAreaField name="notes" label="Notes" fieldId="notes" />
            <output aria-label="Formik value">{String(values.notes)}</output>
            <output aria-label="Formik dirty">{String(dirty)}</output>
          </>
        )}
      </Formik>,
    );

    await user.click(screen.getByRole('textbox', { name: 'Notes' }));
    await user.tab();

    expect(screen.getByLabelText('Formik value')).toHaveTextContent('undefined');
    expect(screen.getByLabelText('Formik dirty')).toHaveTextContent('false');
  });

  it('stores trimmed multiline text while preserving indentation in the draft', async () => {
    const user = userEvent.setup();

    render(
      <Formik initialValues={{ notes: '' }} onSubmit={() => undefined}>
        <>
          <TextAreaField name="notes" label="Notes" fieldId="notes" rows={3} />
          <FormikFieldObserver name="notes" label="Formik notes" />
        </>
      </Formik>,
    );

    const input = screen.getByRole('textbox', { name: 'Notes' });
    const content = '#cloud-config\n  packages:\n    - vim';
    await user.type(input, `\n${content}\n`);
    expect(input).toHaveValue(`\n${content}\n`);
    expect(screen.getByLabelText('Formik notes').textContent).toBe(content);

    await user.tab();
    expect(input).toHaveValue(content);
  });
});
