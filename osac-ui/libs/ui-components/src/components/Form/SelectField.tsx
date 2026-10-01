import { type MouseEvent, type Ref, useEffect, useMemo, useState } from 'react';
import {
  FormGroup,
  MenuToggle,
  type MenuToggleElement,
  Select,
  SelectList,
  SelectOption,
} from '@patternfly/react-core';
import { useField } from 'formik';

import { getVisibleFieldError } from './fieldError';
import { useShowFieldValidationErrors } from './FieldValidationContext';
import { FormFieldHelper, getFormFieldHelperDescribedBy } from './FormFieldHelper';

export interface SelectFieldOption {
  value: string | number;
  label: string;
  isDisabled?: boolean;
  description?: React.ReactNode;
}

export interface SelectFieldProps {
  name: string;
  label: string;
  fieldId: string;
  options: SelectFieldOption[];
  isRequired?: boolean;
  isDisabled?: boolean;
  isLoading?: boolean;
  placeholder?: string;
  loadingPlaceholder?: string;
  /** When true, commits the sole option to Formik once loading finishes and exactly one option exists. */
  autoSelectSingleOption?: boolean;
  helperText?: string;
  onSelect?: (value: string | number) => void;
  asFormGroup?: boolean;
}

export const SelectField = ({
  name,
  label,
  fieldId,
  options,
  isRequired = false,
  isDisabled = false,
  isLoading = false,
  placeholder = '',
  loadingPlaceholder = 'Loading...',
  autoSelectSingleOption = false,
  helperText,
  onSelect: onSelectProp,
  asFormGroup = true,
}: SelectFieldProps) => {
  const [field, meta, helpers] = useField<string | number>(name);
  const [isOpen, setIsOpen] = useState(false);
  const showValidationErrors = useShowFieldValidationErrors();
  const error = getVisibleFieldError(meta, showValidationErrors);
  const validated = error ? 'error' : 'default';
  const helperDescribedBy = getFormFieldHelperDescribedBy(fieldId, error, helperText);
  const effectivePlaceholder = isLoading ? loadingPlaceholder : placeholder;
  const controlDisabled = isDisabled || isLoading;

  useEffect(() => {
    if (
      !autoSelectSingleOption ||
      isLoading ||
      isDisabled ||
      options.length !== 1 ||
      field.value !== ''
    ) {
      return;
    }
    void helpers.setValue(options[0].value, false);
  }, [autoSelectSingleOption, field.value, helpers, isDisabled, isLoading, options]);

  const toggleLabel = useMemo(() => {
    return options.find(({ value }) => value === field.value)?.label ?? effectivePlaceholder;
  }, [effectivePlaceholder, field.value, options]);

  const onSelect = (_event: MouseEvent<Element> | undefined, value: string | number) => {
    helpers.setValue(value, true);
    void helpers.setTouched(true, false);
    onSelectProp?.(value);
    setIsOpen(false);
  };

  const toggle = (toggleRef: Ref<MenuToggleElement>) => (
    <MenuToggle
      ref={toggleRef}
      id={fieldId}
      onClick={() => setIsOpen((wasOpen) => !wasOpen)}
      isExpanded={isOpen}
      isDisabled={controlDisabled}
      isFullWidth
      status={validated === 'error' ? 'danger' : undefined}
      aria-invalid={error ? true : undefined}
      aria-describedby={helperDescribedBy}
      aria-busy={isLoading || undefined}
    >
      {toggleLabel}
    </MenuToggle>
  );

  const select = (
    <>
      <Select
        id={`${fieldId}-select`}
        isOpen={isOpen}
        selected={field.value}
        onSelect={onSelect}
        onOpenChange={setIsOpen}
        toggle={toggle}
        shouldFocusToggleOnSelect
        aria-label={label}
      >
        <SelectList>
          {options.map((option) => (
            <SelectOption
              key={option.value}
              value={option.value}
              isDisabled={option.isDisabled}
              description={option.description}
            >
              {option.label}
            </SelectOption>
          ))}
        </SelectList>
      </Select>
      <FormFieldHelper error={error} description={helperText} fieldId={fieldId} />
    </>
  );

  return asFormGroup ? (
    <FormGroup label={label} fieldId={fieldId} isRequired={isRequired}>
      {select}
    </FormGroup>
  ) : (
    select
  );
};
