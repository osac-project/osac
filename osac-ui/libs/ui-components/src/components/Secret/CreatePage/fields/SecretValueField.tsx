import { useState } from 'react';
import {
  Button,
  FileUpload,
  Flex,
  FlexItem,
  FormGroup,
  Radio,
  Stack,
  StackItem,
  TextArea,
  TextInput,
} from '@patternfly/react-core';
import DownloadIcon from '@patternfly/react-icons/dist/esm/icons/download-icon';
import MinusCircleIcon from '@patternfly/react-icons/dist/esm/icons/minus-circle-icon';
import { useField, useFormikContext } from 'formik';

import {
  FormFieldHelper,
  getFormFieldHelperDescribedBy,
} from '@osac/ui-components/components/Form/FormFieldHelper';

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

type InputMode = 'enter' | 'upload';

interface SecretValueFieldProps {
  entry: SecretDataEntry;
  name: string;
  showKey?: boolean;
  label: string;
  onRemove?: () => void;
  canRemove?: boolean;
}

const SecretValueField = ({
  entry,
  name,
  showKey = false,
  label,
  onRemove,
  canRemove = true,
}: SecretValueFieldProps) => {
  const { t } = useTranslation();
  const { setFieldError, setFieldValue } = useFormikContext<SecretValues>();
  const [field, meta, helpers] = useField<Uint8Array>(`${name}.value`);

  const hasBinaryInitial =
    decodeSecretValue(entry.value) === undefined && entry.value.byteLength > 0;
  const [mode, setMode] = useState<InputMode>(hasBinaryInitial ? 'upload' : 'enter');
  const [fileName, setFileName] = useState(hasBinaryInitial ? entry.key : '');
  const [isUploading, setIsUploading] = useState(false);

  const showValidationErrors = useShowFieldValidationErrors();
  const valueError = getVisibleFieldError(meta, showValidationErrors);
  const value = field.value ?? new Uint8Array();
  const textValue = decodeSecretValue(value);
  const fieldId = `${name.replaceAll('.', '-')}-value`;
  const fileFieldId = `${name.replaceAll('.', '-')}-file`;
  const textHelper = t('Type text here or replace this value with a file.');
  const helperDescribedBy = getFormFieldHelperDescribedBy(fileFieldId, valueError, textHelper);

  const handleFileSelected = async (file: File) => {
    if (file.size > SECRET_FILE_MAX_BYTES) {
      await helpers.setTouched(true);
      setFieldError(`${name}.value`, t('Secret files must not exceed 1 MiB'));
      return;
    }

    try {
      setIsUploading(true);
      await helpers.setValue(new Uint8Array(await file.arrayBuffer()));
      setFileName(file.name);
    } catch {
      setFieldError(`${name}.value`, t('Failed to read secret file'));
    } finally {
      setIsUploading(false);
    }
  };

  const clearValue = async () => {
    await helpers.setValue(new Uint8Array());
    await helpers.setTouched(true);
    setFileName('');
  };

  const handleModeChange = (nextMode: InputMode) => {
    setMode(nextMode);
    void clearValue();
  };

  return (
    <Stack hasGutter>
      <StackItem>
        <FormGroup
          label={showKey ? t('Key') : t('Required key')}
          fieldId={`${fieldId}-required-key`}
          isRequired
        >
          <TextInput
            id={`${fieldId}-required-key`}
            value={entry.key}
            isDisabled={!showKey}
            aria-label={showKey ? t('Key') : t('Required key')}
            onChange={
              showKey
                ? (_event, val) => {
                    void setFieldValue(`${name}.key`, val);
                  }
                : undefined
            }
          />
        </FormGroup>
      </StackItem>
      <StackItem>
        <FormGroup label={t('Value')} fieldId={fieldId} isRequired>
          <Radio
            id={`${fieldId}-enter`}
            name={`${fieldId}-mode`}
            label={t('Enter value')}
            isChecked={mode === 'enter'}
            onChange={() => handleModeChange('enter')}
          />
          <Radio
            id={`${fieldId}-upload`}
            name={`${fieldId}-mode`}
            label={t('Upload file')}
            isChecked={mode === 'upload'}
            onChange={() => handleModeChange('upload')}
          />
        </FormGroup>
      </StackItem>

      <StackItem>
        {mode === 'enter' ? (
          <FormGroup fieldId={`${fieldId}-text`}>
            <Flex>
              <FlexItem grow={{ default: 'grow' }}>
                <TextArea
                  id={`${fieldId}-text`}
                  value={textValue ?? ''}
                  onChange={(_event, nextValue) => {
                    void helpers.setValue(encodeSecretValue(nextValue));
                  }}
                  onBlur={() => void helpers.setTouched(true)}
                  validated={valueError ? 'error' : 'default'}
                  aria-label={label}
                  aria-invalid={!!valueError}
                  autoResize
                />
              </FlexItem>
              {onRemove && (
                <FlexItem alignSelf={{ default: 'alignSelfCenter' }}>
                  <Button
                    variant="plain"
                    aria-label={t('Remove secret entry')}
                    onClick={onRemove}
                    isDisabled={!canRemove || isUploading}
                    icon={<MinusCircleIcon />}
                  />
                </FlexItem>
              )}
            </Flex>
            <FormFieldHelper error={valueError} fieldId={`${fieldId}-text`} />
          </FormGroup>
        ) : (
          <FormGroup fieldId={fileFieldId}>
            <Flex>
              <FlexItem grow={{ default: 'grow' }}>
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
                  aria-label={label}
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
              </FlexItem>
              {onRemove && (
                <FlexItem alignSelf={{ default: 'alignSelfCenter' }}>
                  <Button
                    variant="plain"
                    aria-label={t('Remove secret entry')}
                    onClick={onRemove}
                    isDisabled={!canRemove || isUploading}
                    icon={<MinusCircleIcon />}
                  />
                </FlexItem>
              )}
            </Flex>
            <FormFieldHelper error={valueError} description={textHelper} fieldId={fileFieldId} />
          </FormGroup>
        )}
      </StackItem>
    </Stack>
  );
};

export default SecretValueField;
