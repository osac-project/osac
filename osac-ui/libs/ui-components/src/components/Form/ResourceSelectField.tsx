import { type ReactNode, useEffect, useMemo, useRef } from 'react';
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
  /** Called after a new resource is selected or the field is cleared. Use to trigger cascade resets. */
  onSelectResource?: (value: ResourceSelectValue) => void;
}

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
  const idFieldName = `${name}.id`;
  const nameFieldName = `${name}.name`;
  const { values, setFieldValue } = useFormikContext<Record<string, unknown>>();
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

  const options = useMemo(
    () => items.map((item) => ({ value: item.id, content: item.metadata?.name ?? item.id })),
    [items],
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
        placeholder={effectivePlaceholder}
        onClear={() => void setFieldValue(nameFieldName, '', false)}
      />
    </>
  );
};
