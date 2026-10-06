import { useRef } from 'react';
import {
  FormGroup,
  NumberInput,
  Split,
  SplitItem,
  TextArea,
  TextInput,
} from '@patternfly/react-core';
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
  multiline?: boolean;
  rows?: number;
  resizeOrientation?: 'vertical' | 'horizontal' | 'both' | 'none';
  type?: 'text' | 'number' | 'password';
  helperText?: string;
  placeholder?: string;
  onBlur?: () => void;
  inputMode?: React.HTMLAttributes<HTMLInputElement>['inputMode'];
  min?: number;
  max?: number;
  step?: number;
}

export const InputField = ({
  name,
  label,
  fieldId,
  isRequired = false,
  isDisabled = false,
  multiline = false,
  rows,
  resizeOrientation,
  type = 'text',
  helperText,
  placeholder,
  onBlur,
  inputMode,
  min,
  max,
  step,
  children,
}: React.PropsWithChildren<InputFieldProps>) => {
  const [field, meta, helpers] = useField<string>(name);
  const showValidationErrors = useShowFieldValidationErrors();
  const error = getVisibleFieldError(meta, showValidationErrors);
  const validated = error ? 'error' : 'default';
  const helperDescribedBy = getFormFieldHelperDescribedBy(fieldId, error, helperText);
  const shouldTrimOnBlur = type === 'text' && !multiline;
  const emptyNumberInputOnBlur = useRef(false);

  const handleBlur = (event: React.FocusEvent<HTMLInputElement | HTMLTextAreaElement>) => {
    field.onBlur(event);
    if (shouldTrimOnBlur) {
      const trimmed = (field.value ?? '').trim();
      if (trimmed !== field.value) {
        void helpers.setValue(trimmed);
      }
    }
    onBlur?.();
  };

  const handleNumberChange = (event: React.FormEvent<HTMLInputElement>) => {
    const value = event.currentTarget.value;
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
    onBlur?.();
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

  return (
    <FormGroup label={label} fieldId={fieldId} isRequired={isRequired}>
      {multiline ? (
        <TextArea
          id={fieldId}
          name={name}
          value={field.value ?? ''}
          placeholder={placeholder}
          rows={rows}
          resizeOrientation={resizeOrientation}
          onChange={(_event, value) => {
            void field.onChange({ target: { name, value } });
          }}
          onBlur={handleBlur}
          isDisabled={isDisabled}
          validated={validated}
          aria-invalid={error ? true : undefined}
          aria-describedby={helperDescribedBy}
        />
      ) : (
        <Split hasGutter>
          <SplitItem isFilled>
            {type === 'number' ? (
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
            ) : (
              <TextInput
                id={fieldId}
                name={name}
                type={type}
                inputMode={inputMode}
                value={field.value ?? ''}
                placeholder={placeholder}
                min={min}
                max={max}
                step={step}
                onChange={(_event, value) => {
                  void field.onChange({ target: { name, value } });
                }}
                onBlur={handleBlur}
                isDisabled={isDisabled}
                validated={validated}
                aria-invalid={error ? true : undefined}
                aria-describedby={helperDescribedBy}
              />
            )}
          </SplitItem>
          {children && <SplitItem>{children}</SplitItem>}
        </Split>
      )}
      <FormFieldHelper error={error} description={helperText} fieldId={fieldId} />
    </FormGroup>
  );
};
