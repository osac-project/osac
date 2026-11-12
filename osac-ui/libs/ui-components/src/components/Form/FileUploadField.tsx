import { useState } from 'react';
import { FileUpload, FormGroup } from '@patternfly/react-core';
import { useField } from 'formik';

import { getVisibleFieldError } from './fieldError';
import { useShowFieldValidationErrors } from './FieldValidationContext';
import { FormFieldHelper, getFormFieldHelperDescribedBy } from './FormFieldHelper';
import { useTrimmedInput } from './useTrimmedInput';
import { useTranslation } from '../../hooks/useTranslation';

interface FileUploadFieldProps {
  name: string;
  fieldId: string;
  isDisabled?: boolean;
  helperText?: string;
  placeholder?: string;
}

export const FileUploadField = ({
  name,
  fieldId,
  isDisabled = false,
  helperText,
  placeholder,
}: FileUploadFieldProps) => {
  const { t } = useTranslation();
  const [field, meta, helpers] = useField<string>(name);
  const [filename, setFilename] = useState('');
  const showValidationErrors = useShowFieldValidationErrors();
  const error = getVisibleFieldError(meta, showValidationErrors);
  const helperDescribedBy = getFormFieldHelperDescribedBy(fieldId, error, helperText);
  const trimmedInput = useTrimmedInput(field.value, helpers.setValue);

  return (
    <FormGroup fieldId={fieldId}>
      <FileUpload
        id={fieldId}
        type="text"
        value={trimmedInput.value}
        filename={filename}
        textAreaPlaceholder={placeholder}
        allowEditingUploadedText
        isDisabled={isDisabled}
        validated={error ? 'error' : 'default'}
        aria-label={t('File upload')}
        aria-invalid={error ? true : undefined}
        aria-describedby={helperDescribedBy}
        onFileInputChange={(_event, file) => {
          setFilename(file.name);
        }}
        onTextChange={(_event, text) => {
          trimmedInput.onChange(text);
        }}
        onDataChange={(_event, data) => {
          void helpers.setValue(data);
          void helpers.setTouched(true);
        }}
        onTextAreaBlur={() => {
          void helpers.setTouched(true);
          trimmedInput.onBlur();
        }}
        onClearClick={() => {
          setFilename('');
          void helpers.setValue('');
          void helpers.setTouched(true);
        }}
      />
      <FormFieldHelper error={error} description={helperText} fieldId={fieldId} />
    </FormGroup>
  );
};
