import { FormGroup, Switch } from '@patternfly/react-core';
import { useField } from 'formik';

import { getVisibleFieldError } from './fieldError';
import { useShowFieldValidationErrors } from './FieldValidationContext';
import { FormFieldHelper } from './FormFieldHelper';

interface SwitchFieldProps {
  name: string;
  label: string;
  fieldId: string;
  isDisabled?: boolean;
  helperText?: string;
  isReversed?: boolean;
}

export const SwitchField = ({
  name,
  label,
  fieldId,
  isDisabled = false,
  helperText,
  isReversed = false,
}: SwitchFieldProps) => {
  const [field, meta, helpers] = useField<boolean>(name);
  const showValidationErrors = useShowFieldValidationErrors();
  const error = getVisibleFieldError(meta, showValidationErrors);

  return (
    <FormGroup fieldId={fieldId}>
      <Switch
        id={fieldId}
        label={label}
        isChecked={field.value}
        isDisabled={isDisabled}
        isReversed={isReversed}
        onChange={(_event, checked) => void helpers.setValue(checked)}
        aria-label={label}
      />
      <FormFieldHelper error={error} description={helperText} fieldId={fieldId} />
    </FormGroup>
  );
};
