import { type ReactNode, useEffect, useMemo } from 'react';
import { Alert, Content, FormGroup } from '@patternfly/react-core';
import { TypeaheadSelect } from '@patternfly/react-templates';
import { useField } from 'formik';

import type { ListParams } from '@osac/ui-components/api/types';
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
  request?: Pick<ListParams, 'filter' | 'limit'>;
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
  /** Client-side filter applied after the list response (e.g. GPU-class). */
  filterItems?: (item: ResourceListItem) => boolean;
  /** Label for options and the closed toggle. Defaults to metadata.name. */
  formatOptionLabel?: (item: ResourceListItem) => string;
}

const resourceFromItem = (item: ResourceListItem): ResourceSelectValue => ({
  id: item.id,
  name: item.metadata?.name ?? '',
});

const defaultOptionLabel = (item: ResourceListItem): string => item.metadata?.name ?? item.id;

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
  filterItems,
  formatOptionLabel = defaultOptionLabel,
}: ResourceSelectFieldProps) => {
  const [field, meta, helpers] = useField<ResourceSelectValue>(name);
  const showValidationErrors = useShowFieldValidationErrors();
  const error = getVisibleFieldError(meta, showValidationErrors);
  const { data, isLoading, error: loadError } = useListResource(service, request);
  const items = useMemo(() => {
    const listed = ((data as { items?: ResourceListItem[] } | undefined)?.items ?? []).filter(
      (item) => item.id,
    );
    return filterItems ? listed.filter(filterItems) : listed;
  }, [data, filterItems]);

  const listFailed = Boolean(loadError);
  const listEmpty = !isLoading && !listFailed && items.length === 0;
  const controlDisabled = isDisabled || isLoading || listFailed || listEmpty;
  const effectivePlaceholder = isLoading ? loadingPlaceholder : placeholder;
  const selectedId = field.value?.id ?? '';
  const validated = error ? 'error' : 'default';

  const typeaheadOptions = useMemo(
    () =>
      items.map((item) => ({
        value: item.id,
        content: formatOptionLabel(item),
        selected: item.id === selectedId,
      })),
    [formatOptionLabel, items, selectedId],
  );

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

  const applySelection = (item: ResourceListItem) => {
    const resourceValue = resourceFromItem(item);
    void helpers.setValue(resourceValue, true);
    void helpers.setTouched(true, false);
    onSelectResource?.(resourceValue);
  };

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
        <TypeaheadSelect
          id={`${fieldId}-select`}
          initialOptions={typeaheadOptions}
          isDisabled={controlDisabled}
          placeholder={effectivePlaceholder}
          onSelect={(_event, value) => {
            const item = items.find((resource) => resource.id === value);
            if (!item) {
              return;
            }
            applySelection(item);
          }}
          onClearSelection={() => {
            void helpers.setValue({ id: '', name: '' }, true);
          }}
          toggleProps={{
            id: fieldId,
            'aria-label': label,
            isDisabled: controlDisabled,
            isFullWidth: true,
            status: validated === 'error' ? 'danger' : undefined,
            'aria-invalid': error ? true : undefined,
            'aria-describedby': error ? `${fieldId}-helper-error` : undefined,
            'aria-busy': isLoading || undefined,
          }}
        />
        <FormFieldHelper error={error} fieldId={fieldId} />
      </FormGroup>
    </>
  );
};
