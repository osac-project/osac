import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';
import * as yup from 'yup';

import { MultiSelectField } from './MultiSelectField';

// Flush pending async validation and the React updates it schedules before
// asserting. OSAC-7737 was a race between two such validations.
const flushPendingValidations = () =>
  act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });

const renderMultiSelect = ({
  initialValue = [] as string[],
}: {
  initialValue?: string[];
} = {}) => {
  render(
    <Formik
      initialValues={{ kinds: initialValue }}
      validateOnBlur
      validationSchema={yup.object({
        kinds: yup
          .array()
          .of(yup.string().required())
          .min(1, 'Select at least one kind')
          .required('Select at least one kind'),
      })}
      onSubmit={() => undefined}
    >
      {({ values, submitForm }) => (
        <>
          <MultiSelectField
            name="kinds"
            label="Kinds"
            fieldId="kinds"
            isRequired
            placeholder="Select kinds"
            options={[
              { value: 'small', label: 'Small' },
              { value: 'large', label: 'Large' },
            ]}
          />
          <output aria-label="formik-value">{values.kinds.join(',') || '(empty)'}</output>
          <button type="button" onClick={() => void submitForm()}>
            Submit
          </button>
        </>
      )}
    </Formik>,
  );
};

describe('MultiSelectField', () => {
  it('updates Formik when the user selects an option', async () => {
    const user = userEvent.setup();
    renderMultiSelect();

    await user.click(screen.getByRole('button', { name: 'Kinds' }));
    await user.click(screen.getByRole('option', { name: 'Large' }));

    await waitFor(() => {
      expect(screen.getByLabelText('formik-value')).toHaveTextContent('large');
    });
  });

  it('does not show a required error immediately after selecting an option with validateOnBlur', async () => {
    const user = userEvent.setup();
    renderMultiSelect();

    await user.click(screen.getByRole('button', { name: 'Kinds' }));
    await user.click(screen.getByRole('option', { name: 'Large' }));

    await waitFor(() => {
      expect(screen.getByLabelText('formik-value')).toHaveTextContent('large');
    });
    await flushPendingValidations();

    expect(screen.queryByText('Select at least one kind')).not.toBeInTheDocument();
  });

  it('clears an already-visible required error after selecting an option', async () => {
    const user = userEvent.setup();
    renderMultiSelect();

    await user.click(screen.getByRole('button', { name: 'Submit' }));
    await waitFor(() => {
      expect(screen.getByText('Select at least one kind')).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Kinds' }));
    await user.click(screen.getByRole('option', { name: 'Large' }));
    await waitFor(() => {
      expect(screen.getByLabelText('formik-value')).toHaveTextContent('large');
    });
    await flushPendingValidations();

    expect(screen.queryByText('Select at least one kind')).not.toBeInTheDocument();
  });

  it('shows the required error after deselecting the last selected option', async () => {
    const user = userEvent.setup();
    renderMultiSelect();

    await user.click(screen.getByRole('button', { name: 'Kinds' }));
    await user.click(screen.getByRole('option', { name: 'Large' }));
    await waitFor(() => {
      expect(screen.getByLabelText('formik-value')).toHaveTextContent('large');
    });

    // The multi-select stays open; selecting the option again removes it.
    await user.click(screen.getByRole('option', { name: 'Large' }));

    await waitFor(() => {
      expect(screen.getByLabelText('formik-value')).toHaveTextContent('(empty)');
      expect(screen.getByText('Select at least one kind')).toBeInTheDocument();
    });
  });

  it('validates and marks the field touched when the dropdown closes without a selection', async () => {
    const user = userEvent.setup();
    renderMultiSelect();

    const toggle = screen.getByRole('button', { name: 'Kinds' });
    await user.click(toggle);
    await user.click(toggle);

    await waitFor(() => {
      expect(screen.getByText('Select at least one kind')).toBeInTheDocument();
    });
  });
});
