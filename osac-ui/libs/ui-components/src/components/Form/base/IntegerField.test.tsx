import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Form, Formik } from 'formik';
import { describe, expect, it, vi } from 'vitest';

import IntegerField from './IntegerField';

describe('IntegerField', () => {
  it('stores an empty numeric field as undefined', async () => {
    const onSubmit = vi.fn();
    const user = userEvent.setup();

    render(
      <Formik initialValues={{ cores: 2 }} onSubmit={onSubmit}>
        <Form>
          <IntegerField name="cores" label="Cores" fieldId="cores" />
          <button type="submit">Submit</button>
        </Form>
      </Formik>,
    );

    await user.clear(screen.getByRole('spinbutton', { name: 'Cores' }));
    await user.click(screen.getByRole('button', { name: 'Submit' }));

    expect(onSubmit).toHaveBeenCalledWith({ cores: undefined }, expect.any(Object));
  });

  it('rejects decimal values', () => {
    render(
      <Formik initialValues={{ cores: undefined as number | undefined }} onSubmit={() => undefined}>
        <IntegerField name="cores" label="Cores" fieldId="cores" />
      </Formik>,
    );

    const input = screen.getByRole('spinbutton', { name: 'Cores' });
    expect(input).not.toHaveAttribute('step');
    fireEvent.change(input, { target: { value: '2.5' } });

    expect(input).toHaveValue(null);
  });

  it('rejects negative values', () => {
    render(
      <Formik initialValues={{ count: undefined as number | undefined }} onSubmit={() => undefined}>
        <IntegerField name="count" label="Count" fieldId="count" />
      </Formik>,
    );

    const input = screen.getByRole('spinbutton', { name: 'Count' });

    expect(fireEvent.keyDown(input, { key: '-' })).toBe(false);
    fireEvent.change(input, { target: { value: '-2' } });

    expect(input).toHaveValue(null);
  });
});
