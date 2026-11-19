import { type ReactNode } from 'react';
import { FormGroup, Stack, TextArea, TextAreaProps } from '@patternfly/react-core';
import { useField } from 'formik';

import { getVisibleFieldError } from '../fieldError';
import { useShowFieldValidationErrors } from '../FieldValidationContext';
import { FormFieldHelper, getFormFieldHelperDescribedBy } from '../FormFieldHelper';
import { useTrimmedInput } from '../useTrimmedInput';

interface TextAreaFieldProps {
  name: string;
  label: string;
  fieldId: string;
  isRequired?: boolean;
  isDisabled?: boolean;
  rows?: number;
  resizeOrientation?: TextAreaProps['resizeOrientation'];
  helperText?: string;
  placeholder?: string;
  labelInfo?: ReactNode;
  asFormGroup?: boolean;
}

const TextAreaField = ({
  name,
  label,
  fieldId,
  isRequired = false,
  isDisabled = false,
  rows = 8,
  resizeOrientation = 'vertical',
  helperText,
  placeholder,
  labelInfo,
  asFormGroup = true,
}: TextAreaFieldProps) => {
  const [field, meta, helpers] = useField<string>(name);
  const showValidationErrors = useShowFieldValidationErrors();
  const error = getVisibleFieldError(meta, showValidationErrors);
  const validated = error ? 'error' : 'default';
  const helperDescribedBy = getFormFieldHelperDescribedBy(fieldId, error, helperText);
  const trimmedInput = useTrimmedInput(field.value, helpers.setValue);

  const content = (
    <Stack>
      <TextArea
        id={fieldId}
        name={name}
        value={trimmedInput.value}
        placeholder={placeholder}
        rows={rows}
        resizeOrientation={resizeOrientation}
        aria-label={label}
        onChange={(_event, value) => trimmedInput.onChange(value)}
        onBlur={(event) => {
          field.onBlur(event);
          trimmedInput.onBlur();
        }}
        isDisabled={isDisabled}
        validated={validated}
        aria-invalid={error ? true : undefined}
        aria-describedby={helperDescribedBy}
      />
      <FormFieldHelper error={error} description={helperText} fieldId={fieldId} />
    </Stack>
  );

  return asFormGroup ? (
    <FormGroup label={label} fieldId={fieldId} isRequired={isRequired} labelInfo={labelInfo}>
      {content}
    </FormGroup>
  ) : (
    content
  );
};

export default TextAreaField;
