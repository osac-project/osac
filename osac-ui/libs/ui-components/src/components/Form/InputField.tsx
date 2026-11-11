import { type ReactNode, useRef } from 'react';
import { FormGroup, NumberInput, Split, SplitItem, Stack } from '@patternfly/react-core';
import { useField } from 'formik';

import { getVisibleFieldError } from './fieldError';
import { useShowFieldValidationErrors } from './FieldValidationContext';
import { FormFieldHelper, getFormFieldHelperDescribedBy } from './FormFieldHelper';

interface InputFieldProps {
  name: string;
  label: string;
  fieldId: string;
  isRequired?: boolean;
  isDisabled?: boolean;
  type: 'number';
  helperText?: string;
  placeholder?: string;
  labelInfo?: ReactNode;
  inputMode?: React.HTMLAttributes<HTMLInputElement>['inputMode'];
  min?: number;
  max?: number;
  step?: number;
  asFormGroup?: boolean;
}

export const InputField = ({
  name,
  label,
  fieldId,
  isRequired = false,
  isDisabled = false,
  helperText,
  placeholder,
  labelInfo,
  inputMode,
  min,
  max,
  step,
  children,
  asFormGroup = true,
}: React.PropsWithChildren<InputFieldProps>) => {
  const [field, meta, helpers] = useField<string>(name);
  const showValidationErrors = useShowFieldValidationErrors();
  const error = getVisibleFieldError(meta, showValidationErrors);
  const validated = error ? 'error' : 'default';
  const helperDescribedBy = getFormFieldHelperDescribedBy(fieldId, error, helperText);
  const emptyNumberInputOnBlur = useRef(false);

  const handleNumberChange = (event: React.FormEvent<HTMLInputElement>) => {
    const value = event.currentTarget.value.trim();
    if (emptyNumberInputOnBlur.current && value === '0') {
      emptyNumberInputOnBlur.current = false;
      return;
    }
    emptyNumberInputOnBlur.current = false;
    void field.onChange({ target: { name, value } });
  };

  const handleNumberBlurCapture = (event: React.FocusEvent<HTMLInputElement>) => {
    emptyNumberInputOnBlur.current = event.currentTarget.value === '';
    field.onBlur(event);
  };

  const adjustNumber = (direction: -1 | 1) => {
    const current = Number(field.value);
    const next = (Number.isFinite(current) ? current : 0) + direction * (step ?? 1);
    if ((min !== undefined && next < min) || (max !== undefined && next > max)) {
      return;
    }
    void helpers.setValue(String(next));
  };

  const numberValue =
    field.value === '' || field.value === undefined
      ? ''
      : Number.isFinite(Number(field.value))
        ? Number(field.value)
        : '';

  const textComponent = (
    <Stack>
      <Split hasGutter>
        <SplitItem isFilled>
          <NumberInput
            value={numberValue}
            min={min}
            max={max}
            inputName={name}
            inputAriaLabel={label}
            onMinus={() => adjustNumber(-1)}
            onPlus={() => adjustNumber(1)}
            onChange={handleNumberChange}
            isDisabled={isDisabled}
            validated={validated}
            inputProps={{
              id: fieldId,
              placeholder,
              inputMode,
              min,
              max,
              step,
              onBlurCapture: handleNumberBlurCapture,
              'aria-invalid': error ? true : undefined,
              'aria-describedby': helperDescribedBy,
            }}
          />
        </SplitItem>
        {children && <SplitItem>{children}</SplitItem>}
      </Split>
      <FormFieldHelper error={error} description={helperText} fieldId={fieldId} />
    </Stack>
  );

  return asFormGroup ? (
    <FormGroup label={label} fieldId={fieldId} isRequired={isRequired} labelInfo={labelInfo}>
      {textComponent}
    </FormGroup>
  ) : (
    textComponent
  );
};
