import { Checkbox, FormGroup } from '@patternfly/react-core';
import { useField } from 'formik';

import { getVisibleFieldError } from './fieldError';
import { useShowFieldValidationErrors } from './FieldValidationContext';
import { FormFieldHelper } from './FormFieldHelper';

interface CheckboxFieldProps {
  name: string;
  label: string;
  fieldId: string;
  isDisabled?: boolean;
  helperText?: string;
  /** Called after the Formik value is set with the new checked state. */
  onChange?: (checked: boolean) => void;
}

export const CheckboxField = ({
  name,
  label,
  fieldId,
  isDisabled = false,
  helperText,
  onChange,
}: CheckboxFieldProps) => {
  const [field, meta, helpers] = useField<boolean>(name);
  const showValidationErrors = useShowFieldValidationErrors();
  const error = getVisibleFieldError(meta, showValidationErrors);

  return (
    <FormGroup fieldId={fieldId}>
      <Checkbox
        id={fieldId}
        name={name}
        label={label}
        isChecked={field.value}
        isDisabled={isDisabled}
        onChange={(_event, checked) => {
          void helpers.setValue(checked);
          onChange?.(checked);
        }}
        onBlur={field.onBlur}
      />
      <FormFieldHelper error={error} description={helperText} fieldId={fieldId} />
    </FormGroup>
  );
};
