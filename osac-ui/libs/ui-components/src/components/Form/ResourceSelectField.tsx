import { type MouseEvent, type ReactNode, type Ref, useEffect, useMemo, useState } from 'react';
import {
  Alert,
  Content,
  FormGroup,
  MenuToggle,
  type MenuToggleElement,
  Select,
  SelectList,
  SelectOption,
} from '@patternfly/react-core';
import { useField } from 'formik';

import { useListResource } from '@osac/ui-components/api/use-resource';
import { getErrorMessage } from '@osac/ui-components/utils/error';

import { getVisibleFieldError } from './fieldError';
import { useShowFieldValidationErrors } from './FieldValidationContext';
import { FormFieldHelper } from './FormFieldHelper';
import { type ResourceSelectValue } from './resourceSelectValue';

type ListService = Parameters<typeof useListResource>[0];

export type { ResourceSelectValue } from './resourceSelectValue';
export { emptyResourceSelectValue } from './resourceSelectValue';

export interface ResourceListItem {
  id: string;
  metadata?: { name?: string };
}

export interface ResourceSelectFieldProps {
  name: string;
  label: string;
  fieldId: string;
  service: ListService;
  request?: { filter?: string };
  isRequired?: boolean;
  isDisabled?: boolean;
  labelInfo?: ReactNode;
  placeholder?: string;
  loadingPlaceholder?: string;
  autoSelectSingleOption?: boolean;
  loadErrorTitle?: string;
  emptyTitle?: string;
  emptyDescription?: string;
  /** Called after a new resource is selected. Use to trigger cascade resets. */
  onSelectResource?: (value: ResourceSelectValue) => void;
}

const resourceFromItem = (item: ResourceListItem): ResourceSelectValue => ({
  id: item.id,
  name: item.metadata?.name ?? '',
});

export const ResourceSelectField = ({
  name,
  label,
  fieldId,
  service,
  request,
  isRequired = false,
  isDisabled = false,
  labelInfo,
  placeholder = '',
  loadingPlaceholder = 'Loading...',
  autoSelectSingleOption = false,
  loadErrorTitle,
  emptyTitle,
  emptyDescription,
  onSelectResource,
}: ResourceSelectFieldProps) => {
  const [field, meta, helpers] = useField<ResourceSelectValue>(name);
  const [isOpen, setIsOpen] = useState(false);
  const showValidationErrors = useShowFieldValidationErrors();
  const error = getVisibleFieldError(meta, showValidationErrors);
  const { data, isLoading, error: loadError } = useListResource(service, request);
  const items = useMemo(
    () =>
      ((data as { items?: ResourceListItem[] } | undefined)?.items ?? []).filter((item) => item.id),
    [data],
  );

  const listFailed = Boolean(loadError);
  const listEmpty = !isLoading && !listFailed && items.length === 0;
  const controlDisabled = isDisabled || isLoading || listFailed || listEmpty;
  const effectivePlaceholder = isLoading ? loadingPlaceholder : placeholder;
  const selectedId = field.value?.id ?? '';
  const selectedItem = items.find((item) => item.id === selectedId);
  const toggleLabel =
    (selectedItem ? selectedItem.metadata?.name : field.value?.name) || effectivePlaceholder;
  const validated = error ? 'error' : 'default';

  useEffect(() => {
    if (
      !autoSelectSingleOption ||
      isLoading ||
      controlDisabled ||
      items.length !== 1 ||
      selectedId !== ''
    ) {
      return;
    }
    void helpers.setValue(resourceFromItem(items[0]), false);
  }, [autoSelectSingleOption, controlDisabled, helpers, isLoading, items, selectedId]);

  const onSelect = (_event: MouseEvent<Element> | undefined, value: string | number) => {
    const item = items.find((resource) => resource.id === value);
    if (!item) {
      return;
    }
    const resourceValue = resourceFromItem(item);
    void helpers.setValue(resourceValue, true);
    void helpers.setTouched(true, false);
    setIsOpen(false);
    onSelectResource?.(resourceValue);
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
      aria-describedby={error ? `${fieldId}-helper-error` : undefined}
      aria-busy={isLoading || undefined}
    >
      {toggleLabel}
    </MenuToggle>
  );

  return (
    <>
      {listFailed ? (
        <Alert variant="danger" isInline title={loadErrorTitle}>
          {getErrorMessage(loadError)}
        </Alert>
      ) : null}
      {listEmpty && emptyTitle ? (
        <Alert variant="warning" isInline title={emptyTitle}>
          {emptyDescription ? <Content component="p">{emptyDescription}</Content> : null}
        </Alert>
      ) : null}
      <FormGroup label={label} fieldId={fieldId} isRequired={isRequired} labelInfo={labelInfo}>
        <Select
          id={`${fieldId}-select`}
          isOpen={isOpen}
          selected={selectedId}
          onSelect={onSelect}
          onOpenChange={setIsOpen}
          toggle={toggle}
          shouldFocusToggleOnSelect
        >
          <SelectList>
            {items.map((item) => (
              <SelectOption key={item.id} value={item.id}>
                {item.metadata?.name}
              </SelectOption>
            ))}
          </SelectList>
        </Select>
        <FormFieldHelper error={error} fieldId={fieldId} />
      </FormGroup>
    </>
  );
};
