import { type ReactNode } from 'react';
import { FormGroup, Split, SplitItem, Stack, TextInput } from '@patternfly/react-core';
import { useField } from 'formik';

import { getVisibleFieldError } from '../fieldError';
import { useShowFieldValidationErrors } from '../FieldValidationContext';
import { FormFieldHelper, getFormFieldHelperDescribedBy } from '../FormFieldHelper';
import { useTrimmedInput } from '../useTrimmedInput';

interface TextFieldProps {
  name: string;
  label: string;
  fieldId: string;
  isRequired?: boolean;
  isDisabled?: boolean;
  type?: 'text' | 'password';
  helperText?: string;
  placeholder?: string;
  labelInfo?: ReactNode;
  inputMode?: React.HTMLAttributes<HTMLInputElement>['inputMode'];
  asFormGroup?: boolean;
}

const TextField = ({
  name,
  label,
  fieldId,
  isRequired = false,
  isDisabled = false,
  type = 'text',
  helperText,
  placeholder,
  labelInfo,
  inputMode,
  children,
  asFormGroup = true,
}: React.PropsWithChildren<TextFieldProps>) => {
  const [field, meta, helpers] = useField<string>(name);
  const showValidationErrors = useShowFieldValidationErrors();
  const error = getVisibleFieldError(meta, showValidationErrors);
  const validated = error ? 'error' : 'default';
  const helperDescribedBy = getFormFieldHelperDescribedBy(fieldId, error, helperText);
  const trimmedInput = useTrimmedInput(field.value, helpers.setValue);

  const content = (
    <Stack>
      <Split hasGutter>
        <SplitItem isFilled>
          <TextInput
            id={fieldId}
            name={name}
            type={type}
            inputMode={inputMode}
            value={trimmedInput.value}
            placeholder={placeholder}
            onChange={(_event, value) => trimmedInput.onChange(value)}
            onBlur={(event) => {
              field.onBlur(event);
              trimmedInput.onBlur();
            }}
            isDisabled={isDisabled}
            validated={validated}
            aria-invalid={error ? true : undefined}
            aria-describedby={helperDescribedBy}
            aria-label={label}
          />
        </SplitItem>
        {children && <SplitItem>{children}</SplitItem>}
      </Split>
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

export default TextField;
