import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Form, Formik } from 'formik';
import { describe, expect, it, vi } from 'vitest';

import BigIntField from './BigIntField';

describe('BigIntField', () => {
  it('preserves bigint precision when submitting a large value', async () => {
    const onSubmit = vi.fn();
    const user = userEvent.setup();

    render(
      <Formik initialValues={{ capacityGb: undefined as bigint | undefined }} onSubmit={onSubmit}>
        <Form>
          <BigIntField name="capacityGb" label="Capacity (GiB)" fieldId="capacity-gib" />
          <button type="submit">Submit</button>
        </Form>
      </Formik>,
    );

    await user.type(screen.getByRole('textbox', { name: 'Capacity (GiB)' }), '9007199254740993');
    await user.click(screen.getByRole('button', { name: 'Submit' }));

    expect(onSubmit).toHaveBeenCalledWith({ capacityGb: 9007199254740993n }, expect.any(Object));
  });
});
