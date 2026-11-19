import { render, screen } from '@testing-library/react';
import { Formik } from 'formik';
import { describe, expect, it } from 'vitest';

import DescriptionField from './DescriptionField';

describe('DescriptionField', () => {
  it('renders a three-row textarea labelled Description', () => {
    render(
      <Formik initialValues={{ description: '' }} onSubmit={() => undefined}>
        <DescriptionField />
      </Formik>,
    );

    const input = screen.getByRole('textbox', { name: 'Description' });
    expect(input).toHaveAttribute('rows', '3');
  });

  it('honours custom name and fieldId', () => {
    render(
      <Formik initialValues={{ 'spec.description': '' }} onSubmit={() => undefined}>
        <DescriptionField name="spec.description" fieldId="custom-description" />
      </Formik>,
    );

    expect(screen.getByRole('textbox', { name: 'Description' })).toHaveAttribute(
      'id',
      'custom-description',
    );
  });
});
