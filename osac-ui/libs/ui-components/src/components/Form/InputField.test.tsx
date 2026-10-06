import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Formik, useFormikContext } from 'formik';
import * as FormikModule from 'formik';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { MockInstance } from 'vitest';

import { InputField } from './InputField';

const originalUseField = FormikModule.useField;

const FormikFieldObserver = ({ name, label }: { name: string; label: string }) => {
  const { values } = useFormikContext<Record<string, string>>();
  return <span aria-label={label}>{values[name] ?? ''}</span>;
};

const renderInput = (
  props: Partial<React.ComponentProps<typeof InputField>> = {},
  initialValues: Record<string, string> = { sizeGib: '30' },
) =>
  render(
    <Formik initialValues={initialValues} onSubmit={() => undefined}>
      <InputField name="sizeGib" label="Size (GiB)" fieldId="size-gib" type="number" {...props} />
    </Formik>,
  );

describe('InputField', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('forwards min, max, and step to the number input when provided', () => {
    renderInput({ min: 1, max: 16384, step: 1 });

    const input = screen.getByRole('spinbutton', { name: 'Size (GiB)' });
    expect(input).toHaveAttribute('min', '1');
    expect(input).toHaveAttribute('max', '16384');
    expect(input).toHaveAttribute('step', '1');
  });

  it('renders no min, max, or step attributes when not provided', () => {
    renderInput();

    const input = screen.getByRole('spinbutton', { name: 'Size (GiB)' });
    expect(input).not.toHaveAttribute('min');
    expect(input).not.toHaveAttribute('max');
    expect(input).not.toHaveAttribute('step');
  });

  it('forwards inputMode to the input', () => {
    renderInput({ inputMode: 'numeric' });

    expect(screen.getByRole('spinbutton', { name: 'Size (GiB)' })).toHaveAttribute(
      'inputmode',
      'numeric',
    );
  });

  it('provides PatternFly number controls that update the field', async () => {
    const user = userEvent.setup();

    renderInput();

    await user.click(screen.getByRole('button', { name: 'Plus' }));
    expect(screen.getByRole('spinbutton', { name: 'Size (GiB)' })).toHaveValue(31);

    await user.click(screen.getByRole('button', { name: 'Minus' }));
    expect(screen.getByRole('spinbutton', { name: 'Size (GiB)' })).toHaveValue(30);
  });

  it('keeps an empty number field empty when it loses focus', async () => {
    const user = userEvent.setup();

    renderInput({}, { sizeGib: '' });

    const input = screen.getByRole('spinbutton', { name: 'Size (GiB)' });
    await user.click(input);
    await user.tab();

    expect(input).toHaveValue(null);
  });

  it('trims free-text values on blur', async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();

    render(
      <Formik initialValues={{ name: '  prod-v4  ' }} onSubmit={onSubmit}>
        <InputField name="name" label="Name" fieldId="name" />
      </Formik>,
    );

    const input = screen.getByRole('textbox', { name: 'Name' });
    await user.click(input);
    await user.tab();

    expect(input).toHaveValue('prod-v4');
  });

  it('does not trim number inputs on blur', async () => {
    const user = userEvent.setup();
    let setValueSpy: MockInstance | undefined;
    let latestValues: Record<string, string> = {};
    const useFieldSpy = vi.spyOn(FormikModule, 'useField');
    useFieldSpy.mockImplementation((...args) => {
      const result = originalUseField(...args);
      const [, , helpers] = result;
      setValueSpy = vi.spyOn(helpers, 'setValue');
      return result;
    });

    render(
      <Formik initialValues={{ sizeGib: ' 30 ' }} onSubmit={() => undefined}>
        {(formik) => {
          latestValues = formik.values;
          return (
            <div>
              <InputField name="sizeGib" label="Size (GiB)" fieldId="size-gib" type="number" />
              <FormikFieldObserver name="sizeGib" label="Formik sizeGib" />
            </div>
          );
        }}
      </Formik>,
    );

    const input = screen.getByRole('spinbutton', { name: 'Size (GiB)' });
    await user.click(input);
    await user.tab();

    if (setValueSpy === undefined) {
      throw new Error('setValue spy was not attached');
    }
    expect(setValueSpy).not.toHaveBeenCalled();
    expect(latestValues.sizeGib).toBe(screen.getByLabelText('Formik sizeGib').textContent);
  });

  it('does not trim multiline text on blur', async () => {
    const user = userEvent.setup();

    render(
      <Formik initialValues={{ notes: '  hello  ' }} onSubmit={() => undefined}>
        <InputField name="notes" label="Notes" fieldId="notes" multiline rows={3} />
      </Formik>,
    );

    const input = screen.getByRole('textbox', { name: 'Notes' });
    await user.click(input);
    await user.tab();

    expect(input).toHaveValue('  hello  ');
  });
});
