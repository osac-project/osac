import { type ReactNode, useMemo } from 'react';
import { FormGroup } from '@patternfly/react-core';
import { TypeaheadSelect, type TypeaheadSelectOption } from '@patternfly/react-templates';
import { useField } from 'formik';

import { getVisibleFieldError } from './fieldError';
import { useShowFieldValidationErrors } from './FieldValidationContext';
import { FormFieldHelper } from './FormFieldHelper';

interface TypeaheadSelectFieldProps {
  name: string;
  label: string;
  fieldId: string;
  options: TypeaheadSelectOption[];
  isRequired?: boolean;
  isDisabled?: boolean;
  labelInfo?: ReactNode;
  placeholder?: string;
  noOptionsFoundMessage?: (filter: string) => string;
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
  placeholder,
  noOptionsFoundMessage,
}: TypeaheadSelectFieldProps) => {
  const [field, meta, helpers] = useField<string>(name);
  const showValidationErrors = useShowFieldValidationErrors();
  const error = getVisibleFieldError(meta, showValidationErrors);

  const selectableOptions = useMemo(
    () => options.map((option) => ({ ...option, selected: field.value === option.value })),
    [options, field.value],
  );

  return (
    <FormGroup label={label} fieldId={fieldId} isRequired={isRequired} labelInfo={labelInfo}>
      <TypeaheadSelect
        id={fieldId}
        initialOptions={selectableOptions}
        isDisabled={isDisabled}
        placeholder={placeholder}
        noOptionsFoundMessage={noOptionsFoundMessage}
        onSelect={async (_event, value) => {
          await helpers.setValue(String(value), true);
          await helpers.setTouched(true, false);
        }}
        onClearSelection={() => {
          void helpers.setValue('', true);
        }}
        toggleProps={{
          id: fieldId,
          'aria-label': label,
          isFullWidth: true,
          status: error ? 'danger' : undefined,
        }}
      />
      <FormFieldHelper error={error} fieldId={fieldId} />
    </FormGroup>
  );
};
