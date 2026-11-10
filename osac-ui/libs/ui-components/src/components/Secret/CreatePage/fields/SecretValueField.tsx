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
} from '@patternfly/react-core';
import DownloadIcon from '@patternfly/react-icons/dist/esm/icons/download-icon';
import MinusCircleIcon from '@patternfly/react-icons/dist/esm/icons/minus-circle-icon';
import { useField, useFormikContext } from 'formik';

import {
  FormFieldHelper,
  getFormFieldHelperDescribedBy,
} from '@osac/ui-components/components/Form/FormFieldHelper';
import { InputField } from '@osac/ui-components/components/Form/InputField';

import { useTranslation } from '../../../../hooks/useTranslation';
import { getVisibleFieldError } from '../../../Form/fieldError';
import { useShowFieldValidationErrors } from '../../../Form/FieldValidationContext';
import { useTrimmedInput } from '../../../Form/useTrimmedInput';
import { downloadSecretBytes, getSecretType } from '../../utils';
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
  keyEditable?: boolean;
  onRemove?: () => void;
  canRemove?: boolean;
}

const SecretValueField = ({
  entry,
  name,
  keyEditable = false,
  onRemove,
  canRemove = true,
}: SecretValueFieldProps) => {
  const { t } = useTranslation();
  const { setFieldError, values } = useFormikContext<SecretValues>();
  const secretTypes = getSecretType(t);
  const valueLabel = keyEditable ? t('Value') : secretTypes[values.type];
  const [keyField] = useField<string>(`${name}.key`);
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
  const trimmedInput = useTrimmedInput(textValue, (nextValue) => {
    void helpers.setValue(encodeSecretValue(nextValue));
  });
  const keyFieldId = `${name.replaceAll('.', '-')}-key`;
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
      const fileBytes = new Uint8Array(await file.arrayBuffer());
      trimmedInput.onBlur();
      await helpers.setValue(fileBytes);
      setFileName(file.name);
    } catch {
      setFieldError(`${name}.value`, t('Failed to read secret file'));
    } finally {
      setIsUploading(false);
    }
  };

  const clearValue = async () => {
    trimmedInput.onBlur();
    await helpers.setValue(new Uint8Array());
    await helpers.setTouched(true);
    setFileName('');
  };

  const handleModeChange = (nextMode: InputMode) => {
    setMode(nextMode);
    void clearValue();
  };

  const handleTextBlur = () => {
    trimmedInput.onBlur();
    void helpers.setTouched(true);
  };

  return (
    <Stack hasGutter>
      <StackItem>
        <InputField
          label={keyEditable ? t('Key') : t('Required key')}
          fieldId={keyFieldId}
          isRequired
          name={`${name}.key`}
          isDisabled={!keyEditable}
        />
      </StackItem>
      <StackItem>
        <FormGroup label={valueLabel} fieldId={fieldId} isRequired>
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
                  value={trimmedInput.value}
                  onChange={(_event, nextValue) => trimmedInput.onChange(nextValue)}
                  onBlur={handleTextBlur}
                  validated={valueError ? 'error' : 'default'}
                  aria-label={valueLabel}
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
                  value={trimmedInput.value}
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
                  aria-label={valueLabel}
                  aria-invalid={valueError ? true : undefined}
                  aria-describedby={helperDescribedBy}
                  browseButtonAriaDescribedby={helperDescribedBy}
                  onFileInputChange={(_event, file) => {
                    void handleFileSelected(file);
                  }}
                  onTextChange={(_event, nextValue) => trimmedInput.onChange(nextValue)}
                  onTextAreaBlur={handleTextBlur}
                  onClearClick={() => void clearValue()}
                >
                  {value.byteLength > 0 && (
                    <Button
                      variant="link"
                      icon={<DownloadIcon />}
                      onClick={() => downloadSecretBytes(value, keyField.value)}
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
