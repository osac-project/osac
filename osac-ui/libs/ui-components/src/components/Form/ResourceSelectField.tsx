import { type ReactElement, type ReactNode, useEffect, useMemo, useRef } from 'react';
import { Alert, Content } from '@patternfly/react-core';
import { getIn, useFormikContext } from 'formik';

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
}

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
}: ResourceSelectFieldProps<TResource>) => {
  const idFieldName = `${name}.id`;
  const nameFieldName = `${name}.name`;
  const { values, setFieldValue } = useFormikContext<Record<string, unknown>>();
  const { data, isLoading, error: loadError } = useListResource(service, request);
  const items = useMemo(
    () => ((data as { items?: TResource[] } | undefined)?.items ?? []).filter((item) => item.id),
    [data],
  );

  const listFailed = Boolean(loadError);
  const listEmpty = !isLoading && !listFailed && items.length === 0;
  const controlDisabled = isDisabled || isLoading || listFailed || listEmpty;
  const effectivePlaceholder = isLoading ? loadingPlaceholder : placeholder;

  const selectedId = (getIn(values, idFieldName) as string | undefined) ?? '';

  // Keep the resource's name in sync with its id, and resolve the id to the resource name
  // whenever a resource is picked, auto-selected, or cleared.
  const isFirstRender = useRef(true);
  useEffect(() => {
    if (isFirstRender.current) {
      isFirstRender.current = false;
      return;
    }
    const item = items.find((resource) => resource.id === selectedId);
    const resourceName = item?.metadata?.name ?? '';
    void setFieldValue(nameFieldName, resourceName, false);
    onSelectResource?.(
      selectedId ? { id: selectedId, name: resourceName } : emptyResourceSelectValue(),
      item,
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedId]);

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
  }, [
    autoSelectSingleOption,
    controlDisabled,
    idFieldName,
    isLoading,
    items,
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
  }, [
    autoSelectFirstOption,
    controlDisabled,
    idFieldName,
    isLoading,
    items,
    selectedId,
    setFieldValue,
  ]);

  const options = useMemo(
    () =>
      items.map((item) => ({
        value: item.id,
        content: item.metadata?.name ?? '',
        description: showOptionDescriptions ? item.metadata?.description : undefined,
      })),
    [items, showOptionDescriptions],
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
      />
    </>
  );
};
