import { type ReactElement, type ReactNode, useMemo } from 'react';
import { FormGroup, TextInput } from '@patternfly/react-core';
import { TypeaheadSelect, type TypeaheadSelectOption } from '@patternfly/react-templates';
import { useField } from 'formik';

import { getVisibleFieldError } from './fieldError';
import { useShowFieldValidationErrors } from './FieldValidationContext';
import { FormFieldHelper, getFormFieldHelperDescribedBy } from './FormFieldHelper';

import './TypeaheadSelectField.css';

interface TypeaheadSelectFieldProps {
  name: string;
  label: string;
  fieldId: string;
  options: TypeaheadSelectOption[];
  isRequired?: boolean;
  isDisabled?: boolean;
  labelInfo?: ReactNode;
  labelHelp?: ReactElement;
  placeholder?: string;
  description?: string;
  noOptionsFoundMessage?: (filter: string) => string;
  onSelectionChange?: (value: string) => void;
}

// Generic Formik-bound typeahead single-select (mirrors SelectField). Owns the
// Formik wiring and validation display; the caller supplies the options.
export const TypeaheadSelectField = ({
  name,
  label,
  fieldId,
  options,
  isRequired = false,
  isDisabled = false,
  labelInfo,
  labelHelp,
  placeholder,
  description,
  noOptionsFoundMessage,
  onSelectionChange,
}: TypeaheadSelectFieldProps) => {
  const [field, meta, helpers] = useField<string>(name);
  const showValidationErrors = useShowFieldValidationErrors();
  const error = getVisibleFieldError(meta, showValidationErrors);
  const helperDescribedBy = getFormFieldHelperDescribedBy(fieldId, error, description);

  const selectableOptions = useMemo(
    () => options.map((option) => ({ ...option, selected: field.value === option.value })),
    [options, field.value],
  );
  const selectedOption = selectableOptions.find((option) => option.selected);
  const selectedLabel = selectedOption?.content ?? String(field.value ?? '');

  return (
    <FormGroup
      className={isRequired ? 'osac-typeahead-select--required' : undefined}
      label={label}
      fieldId={fieldId}
      isRequired={isRequired}
      labelInfo={labelInfo}
      labelHelp={labelHelp}
    >
      {isDisabled ? (
        <TextInput
          id={fieldId}
          role="combobox"
          aria-label={label}
          aria-expanded={false}
          aria-describedby={helperDescribedBy}
          value={selectedLabel}
          placeholder={placeholder}
          isDisabled
          readOnly
        />
      ) : (
        <TypeaheadSelect
          id={fieldId}
          initialOptions={selectableOptions}
          placeholder={placeholder}
          noOptionsFoundMessage={noOptionsFoundMessage}
          onSelect={async (_event, value) => {
            const selectedValue = String(value);
            await helpers.setValue(selectedValue, true);
            await helpers.setTouched(true, false);
            onSelectionChange?.(selectedValue);
          }}
          onClearSelection={
            isRequired
              ? undefined
              : () => {
                  void helpers.setValue('', true);
                  onSelectionChange?.('');
                }
          }
          toggleProps={{
            id: fieldId,
            'aria-label': label,
            isFullWidth: true,
            status: error ? 'danger' : undefined,
            'aria-describedby': helperDescribedBy,
          }}
        />
      )}
      <FormFieldHelper error={error} description={description} fieldId={fieldId} />
    </FormGroup>
  );
};
