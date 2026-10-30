import { type ReactElement, type ReactNode, useCallback, useEffect, useMemo } from 'react';
import { Alert, Content } from '@patternfly/react-core';
import { getIn, setIn, useFormikContext } from 'formik';

import type { ListParams } from '@osac/ui-components/api/types';
import { useListResource } from '@osac/ui-components/api/use-resource';
import { getErrorMessage } from '@osac/ui-components/utils/error';

import { type ResourceSelectValue, emptyResourceSelectValue } from './resourceSelectValue';
import { TypeaheadSelectField } from './TypeaheadSelectField';

type ListService = Parameters<typeof useListResource>[0];

export type { ResourceSelectValue } from './resourceSelectValue';
export { emptyResourceSelectValue } from './resourceSelectValue';

export interface ResourceListItem {
  id: string;
  metadata?: { name?: string; description?: string };
}

export interface ResourceSelectFieldProps<TResource extends ResourceListItem = ResourceListItem> {
  name: string;
  label: string;
  fieldId: string;
  service: ListService;
  request?: Pick<ListParams, 'filter' | 'limit'>;
  isRequired?: boolean;
  isDisabled?: boolean;
  labelInfo?: ReactNode;
  labelHelp?: ReactElement;
  description?: string;
  placeholder?: string;
  loadingPlaceholder?: string;
  autoSelectSingleOption?: boolean;
  loadErrorTitle?: string;
  emptyTitle?: string;
  emptyDescription?: string;
  autoSelectFirstOption?: boolean;
  showOptionDescriptions?: boolean;
  /** Called after a new resource is selected or the field is cleared. Use to trigger cascade resets. */
  onSelectResource?: (value: ResourceSelectValue, resource?: TResource) => void;
  /** Client-side filter applied after the list response (e.g. GPU-class). */
  filterItems?: (item: TResource) => boolean;
  /** Label for options and the closed toggle. Defaults to metadata.name. */
  formatOptionLabel?: (item: TResource) => string;
}

const defaultOptionLabel = (item: ResourceListItem): string => item.metadata?.name ?? item.id;

export const ResourceSelectField = <TResource extends ResourceListItem = ResourceListItem>({
  name,
  label,
  fieldId,
  service,
  request,
  isRequired = false,
  isDisabled = false,
  labelInfo,
  labelHelp,
  description,
  placeholder = '',
  loadingPlaceholder = 'Loading...',
  autoSelectSingleOption = false,
  loadErrorTitle,
  emptyTitle,
  emptyDescription,
  autoSelectFirstOption = false,
  showOptionDescriptions = true,
  onSelectResource,
  filterItems,
  formatOptionLabel = defaultOptionLabel,
}: ResourceSelectFieldProps<TResource>) => {
  const idFieldName = `${name}.id`;
  const nameFieldName = `${name}.name`;
  const { dirty, resetForm, values, setFieldValue } = useFormikContext<Record<string, unknown>>();
  const { data, isLoading, error: loadError } = useListResource(service, request);
  const items = useMemo(() => {
    const listed = ((data as { items?: TResource[] } | undefined)?.items ?? []).filter(
      (item) => item.id,
    );
    return filterItems ? listed.filter(filterItems) : listed;
  }, [data, filterItems]);

  const listFailed = Boolean(loadError);
  const listEmpty = !isLoading && !listFailed && items.length === 0;
  const controlDisabled = isDisabled || isLoading || listFailed || listEmpty;
  const effectivePlaceholder = isLoading ? loadingPlaceholder : placeholder;

  const selectedId = (getIn(values, idFieldName) as string | undefined) ?? '';

  const notifyResourceSelection = useCallback(
    (resourceId: string) => {
      const item = items.find((resource) => resource.id === resourceId);
      const resourceName = item?.metadata?.name ?? '';
      void setFieldValue(nameFieldName, resourceName, false);
      onSelectResource?.(
        resourceId ? { id: resourceId, name: resourceName } : emptyResourceSelectValue(),
        item,
      );
    },
    [items, nameFieldName, onSelectResource, setFieldValue],
  );

  // Keep the resource's name in sync with its id, including when a resource list
  // finishes loading after an initial id was provided.
  useEffect(() => {
    const item = items.find((resource) => resource.id === selectedId);
    if (selectedId && !item) {
      return;
    }
    const resourceName = item?.metadata?.name ?? '';
    if (getIn(values, nameFieldName) !== resourceName) {
      const nextValues = setIn(values, nameFieldName, resourceName) as Record<string, unknown>;
      if (dirty) {
        void setFieldValue(nameFieldName, resourceName, false);
      } else {
        resetForm({ values: nextValues });
      }
    }
  }, [dirty, items, nameFieldName, resetForm, selectedId, setFieldValue, values]);

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
    void setFieldValue(idFieldName, items[0].id, false);
    notifyResourceSelection(items[0].id);
  }, [
    autoSelectSingleOption,
    controlDisabled,
    idFieldName,
    isLoading,
    items,
    notifyResourceSelection,
    selectedId,
    setFieldValue,
  ]);

  useEffect(() => {
    if (
      !autoSelectFirstOption ||
      isLoading ||
      controlDisabled ||
      items.length === 0 ||
      selectedId !== ''
    ) {
      return;
    }
    void setFieldValue(idFieldName, items[0].id, true);
    notifyResourceSelection(items[0].id);
  }, [
    autoSelectFirstOption,
    controlDisabled,
    idFieldName,
    isLoading,
    items,
    notifyResourceSelection,
    selectedId,
    setFieldValue,
  ]);

  const options = useMemo(
    () =>
      items.map((item) => ({
        value: item.id,
        content: formatOptionLabel(item),
        description: showOptionDescriptions ? item.metadata?.description : undefined,
      })),
    [formatOptionLabel, items, showOptionDescriptions],
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
      <TypeaheadSelectField
        name={idFieldName}
        label={label}
        fieldId={fieldId}
        options={options}
        isRequired={isRequired}
        isDisabled={controlDisabled}
        labelInfo={labelInfo}
        labelHelp={labelHelp}
        description={description}
        placeholder={effectivePlaceholder}
        onSelectionChange={notifyResourceSelection}
      />
    </>
  );
};
