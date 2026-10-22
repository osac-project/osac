import type { ReactNode } from 'react';
import { FormGroup, Radio } from '@patternfly/react-core';
import { useField } from 'formik';

import { getVisibleFieldError } from './fieldError';
import { useShowFieldValidationErrors } from './FieldValidationContext';
import { FormFieldHelper } from './FormFieldHelper';

export interface RadioButtonFieldOption {
  value: string;
  label: string;
}

interface RadioButtonFieldProps {
  name: string;
  label: string;
  fieldId: string;
  options: RadioButtonFieldOption[];
  isRequired?: boolean;
  isDisabled?: boolean;
  isInline?: boolean;
  labelInfo?: ReactNode;
}

export const RadioButtonField = ({
  name,
  label,
  fieldId,
  options,
  isRequired = false,
  isDisabled = false,
  isInline = false,
  labelInfo,
}: RadioButtonFieldProps) => {
  const [field, meta] = useField<string | boolean>(name);
  const showValidationErrors = useShowFieldValidationErrors();
  const error = getVisibleFieldError(meta, showValidationErrors);
  const stringValue =
    field.value === true ? 'true' : field.value === false ? 'false' : String(field.value ?? '');

  return (
    <FormGroup
      label={label}
      fieldId={fieldId}
      isRequired={isRequired}
      labelInfo={labelInfo}
      role="radiogroup"
      isInline={isInline}
    >
      {options.map((option) => (
        <Radio
          key={option.value}
          id={`${fieldId}-${option.value}`}
          name={name}
          label={option.label}
          isChecked={stringValue === option.value}
          isDisabled={isDisabled}
          onChange={() => {
            const parsed =
              option.value === 'true' ? true : option.value === 'false' ? false : option.value;
            void field.onChange({ target: { name, value: parsed } });
          }}
          onBlur={field.onBlur}
        />
      ))}
      <FormFieldHelper error={error} fieldId={fieldId} />
    </FormGroup>
  );
};
