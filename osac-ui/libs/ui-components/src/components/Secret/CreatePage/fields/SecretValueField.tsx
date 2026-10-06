import { useState } from 'react';
import { Button, FileUpload, FormGroup } from '@patternfly/react-core';
import DownloadIcon from '@patternfly/react-icons/dist/esm/icons/download-icon';
import { useField, useFormikContext } from 'formik';

import {
  FormFieldHelper,
  getFormFieldHelperDescribedBy,
} from '@osac/ui-components/components/Form/FormFieldHelper';
import { InputField } from '@osac/ui-components/components/Form/InputField';

import { useTranslation } from '../../../../hooks/useTranslation';
import { getVisibleFieldError } from '../../../Form/fieldError';
import { useShowFieldValidationErrors } from '../../../Form/FieldValidationContext';
import { downloadSecretBytes } from '../../utils';
import {
  SECRET_FILE_MAX_BYTES,
  type SecretDataEntry,
  type SecretValues,
  decodeSecretValue,
  encodeSecretValue,
} from '../values';

interface SecretValueFieldProps {
  entry: SecretDataEntry;
  name: string;
  showKey?: boolean;
  label: string;
}

const SecretValueField = ({ entry, name, showKey = false, label }: SecretValueFieldProps) => {
  const { t } = useTranslation();
  const { setFieldError } = useFormikContext<SecretValues>();
  const [field, meta, helpers] = useField<Uint8Array>(`${name}.value`);
  const [fileName, setFileName] = useState(
    decodeSecretValue(entry.value) === undefined && entry.value.byteLength > 0 ? entry.key : '',
  );
  const showValidationErrors = useShowFieldValidationErrors();
  const valueError = getVisibleFieldError(meta, showValidationErrors);
  const value = field.value ?? new Uint8Array();
  const textValue = decodeSecretValue(value);
  const fileFieldId = `${name.replaceAll('.', '-')}-file`;
  const textHelper =
    textValue === undefined
      ? t(
          'This value contains binary data and cannot be edited as text. Download it or replace it with a text-compatible file.',
        )
      : t('Type text here or replace this value with a file.');
  const helperDescribedBy = getFormFieldHelperDescribedBy(fileFieldId, valueError, textHelper);

  const handleFileSelected = async (file: File) => {
    if (file.size > SECRET_FILE_MAX_BYTES) {
      await helpers.setTouched(true);
      setFieldError(`${name}.value`, t('Secret files must not exceed 1 MiB'));
      return;
    }

    try {
      await helpers.setValue(new Uint8Array(await file.arrayBuffer()));
      setFileName(file.name);
    } catch {
      setFieldError(`${name}.value`, t('Failed to read secret file'));
    }
  };

  const clearValue = async () => {
    await helpers.setValue(new Uint8Array());
    await helpers.setTouched(true);
    setFileName('');
  };

  return (
    <>
      {showKey && (
        <InputField
          name={`${name}.key`}
          fieldId={`${name.replaceAll('.', '-')}-key`}
          label={t('Key')}
          isRequired
        />
      )}
      <FormGroup label={label} fieldId={fileFieldId} isRequired>
        <FileUpload
          id={fileFieldId}
          type="text"
          value={textValue ?? ''}
          filename={fileName}
          browseButtonText={t('Choose file')}
          clearButtonText={t('Clear value')}
          filenameAriaLabel={t('Selected secret file')}
          filenamePlaceholder={t('No file selected')}
          textAreaPlaceholder={t('Type text here or replace this value with a file.')}
          allowEditingUploadedText
          hideDefaultPreview={textValue === undefined}
          isRequired
          validated={valueError ? 'error' : 'default'}
          aria-label={t('Value')}
          aria-invalid={valueError ? true : undefined}
          aria-describedby={helperDescribedBy}
          browseButtonAriaDescribedby={helperDescribedBy}
          onFileInputChange={(_event, file) => {
            void handleFileSelected(file);
          }}
          onTextChange={(_event, nextValue) => {
            void helpers.setValue(encodeSecretValue(nextValue));
          }}
          onTextAreaBlur={() => void helpers.setTouched(true)}
          onClearClick={() => void clearValue()}
        >
          {value.byteLength > 0 && (
            <Button
              variant="link"
              icon={<DownloadIcon />}
              onClick={() => downloadSecretBytes(value, entry.key)}
            >
              {t('Download current value')}
            </Button>
          )}
        </FileUpload>
        <FormFieldHelper error={valueError} description={textHelper} fieldId={fileFieldId} />
      </FormGroup>
    </>
  );
};

export default SecretValueField;
