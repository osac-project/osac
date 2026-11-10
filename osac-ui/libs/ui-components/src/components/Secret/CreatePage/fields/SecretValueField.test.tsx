import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import { SecretType } from '@osac/types';

import SecretValueField from './SecretValueField';
import { type SecretValues, createSecretDataEntry, encodeSecretValue } from '../values';

describe('SecretValueField', () => {
  it('stores trimmed text as UTF-8 bytes while keeping the draft visible until blur', async () => {
    const user = userEvent.setup();
    const entry = createSecretDataEntry('token', encodeSecretValue('  original  '));
    const initialValues: SecretValues = {
      metadata: { name: '', project: '' },
      type: SecretType.OPAQUE,
      data: [entry],
    };
    let currentValues = initialValues;
    let valueTouched = false;

    render(
      <Formik initialValues={initialValues} onSubmit={() => undefined}>
        {({ values, touched }) => {
          currentValues = values;
          valueTouched = Boolean(touched.data?.[0]?.value);
          return <SecretValueField entry={entry} name="data.0" keyEditable />;
        }}
      </Formik>,
    );

    const input = screen.getByRole('textbox', { name: 'Value' });
    expect(input).toHaveValue('  original  ');
    expect(currentValues.data[0].value).toEqual(encodeSecretValue('  original  '));

    await user.clear(input);
    await user.type(input, '  café{enter}  line  ');

    const text = '  café\n  line  ';
    expect(input).toHaveValue(text);
    expect(currentValues.data[0].value).toEqual(encodeSecretValue('café\n  line'));

    await user.tab();
    expect(input).toHaveValue('café\n  line');
    expect(valueTouched).toBe(true);
  });

  it('trims text edited in FileUpload while keeping the draft visible until blur', async () => {
    const user = userEvent.setup();
    const entry = createSecretDataEntry('token', new Uint8Array());
    const initialValues: SecretValues = {
      metadata: { name: '', project: '' },
      type: SecretType.OPAQUE,
      data: [entry],
    };
    let currentValue = entry.value;

    render(
      <Formik initialValues={initialValues} onSubmit={() => undefined}>
        {({ values }) => {
          currentValue = values.data[0].value;
          return <SecretValueField entry={entry} name="data.0" keyEditable />;
        }}
      </Formik>,
    );

    await user.click(screen.getByRole('radio', { name: 'Upload file' }));
    const input = screen.getByRole('textbox', { name: 'Value' });
    await user.type(input, '  café{enter}  line  ');

    expect(input).toHaveValue('  café\n  line  ');
    expect(currentValue).toEqual(encodeSecretValue('café\n  line'));

    await user.tab();
    expect(input).toHaveValue('café\n  line');
  });
});
