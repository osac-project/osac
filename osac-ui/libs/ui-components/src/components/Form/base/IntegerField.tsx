import { FormGroup, Stack, TextInput } from '@patternfly/react-core';
import { useField } from 'formik';

import { INT32_MAX } from '@osac/ui-components/validation/number';

import { getVisibleFieldError } from '../fieldError';
import { useShowFieldValidationErrors } from '../FieldValidationContext';
import { FormFieldHelper, getFormFieldHelperDescribedBy } from '../FormFieldHelper';
import { useTrimmedInput } from '../useTrimmedInput';
import { INVALID_NUMBER_INPUT_KEYS, NON_NEGATIVE_NUMBER_INPUT_PATTERN } from './utils';

interface IntegerFieldProps {
  name: string;
  label: string;
  fieldId: string;
  isRequired?: boolean;
  isDisabled?: boolean;
  helperText?: string;
  placeholder?: string;
  asFormGroup?: boolean;
  labelInfo?: React.ReactNode;
}

const IntegerField = ({
  name,
  label,
  fieldId,
  isRequired = false,
  isDisabled = false,
  helperText,
  placeholder,
  asFormGroup = true,
  labelInfo,
}: React.PropsWithChildren<IntegerFieldProps>) => {
  const [field, meta, helpers] = useField<number | undefined>(name);
  const trimmedInput = useTrimmedInput(field.value?.toString(), (value) => {
    void helpers.setValue(value === '' ? undefined : Number(value));
  });
  const showValidationErrors = useShowFieldValidationErrors();
  const error = getVisibleFieldError(meta, showValidationErrors);
  const validated = error ? 'error' : 'default';
  const helperDescribedBy = getFormFieldHelperDescribedBy(fieldId, error, helperText);

  const content = (
    <Stack>
      <TextInput
        id={fieldId}
        name={name}
        type="number"
        inputMode="numeric"
        value={trimmedInput.value}
        placeholder={placeholder}
        min={0}
        max={INT32_MAX}
        onKeyDown={(event) => {
          if (INVALID_NUMBER_INPUT_KEYS.has(event.key)) {
            event.preventDefault();
          }
        }}
        onChange={(event, value) => {
          if (!NON_NEGATIVE_NUMBER_INPUT_PATTERN.test(value)) {
            event.currentTarget.value = trimmedInput.value;
            return;
          }
          trimmedInput.onChange(value);
        }}
        onBlur={(event) => {
          field.onBlur(event);
          trimmedInput.onBlur();
        }}
        isDisabled={isDisabled}
        validated={validated}
        aria-label={label}
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

export default IntegerField;
