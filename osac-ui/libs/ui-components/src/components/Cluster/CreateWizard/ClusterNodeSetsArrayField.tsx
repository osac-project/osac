import { useEffect, useMemo } from 'react';
import { ActionGroup, Alert, Button, Stack, StackItem } from '@patternfly/react-core';
import MinusCircleIcon from '@patternfly/react-icons/dist/esm/icons/minus-circle-icon';
import PlusCircleIcon from '@patternfly/react-icons/dist/esm/icons/plus-circle-icon';
import { useField, useFormikContext } from 'formik';

import { BareMetalInstanceTypes } from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';

import { type ClusterNodeSetRow, createEmptyNodeSetRow } from './fields';
import { useTranslation } from '../../../hooks/useTranslation';
import { getErrorMessage } from '../../../utils/error';
import ClusterPoolSizeField from '../../Form/ClusterPoolSizeField';
import NameField from '../../Form/NameField';
import { SelectField } from '../../Form/SelectField';
import ProvisioningFieldGroup from '../../ProvisioningWizard/fields/ProvisioningFieldGroup';
import type { ProvisioningFieldControlProps } from '../../ProvisioningWizard/fields/provisioningFieldValue';

const ClusterNodeSetsArrayField = ({
  name = '',
  isDisabled = false,
}: ProvisioningFieldControlProps) => {
  const { t } = useTranslation();
  const { setFieldValue } = useFormikContext();
  const [field, , helpers] = useField<ClusterNodeSetRow[]>(name);
  const rows = useMemo(() => field.value ?? [], [field.value]);
  const {
    data: hostTypes,
    isLoading: hostTypesLoading,
    error: hostTypesError,
    refetch: refetchHostTypes,
  } = useListResource(BareMetalInstanceTypes);

  const hostTypeItems = useMemo(() => hostTypes?.items ?? [], [hostTypes?.items]);
  const hostTypeOptions = hostTypeItems.map((hostType) => ({
    value: hostType.id,
    label: hostType.metadata?.name || '',
  }));

  useEffect(() => {
    if (hostTypesLoading || hostTypeItems.length === 0) {
      return;
    }
    let changed = false;
    const nextRows = rows.map((row) => {
      const currentValue = row.hostType.trim();
      if (!currentValue || hostTypeItems.some((hostType) => hostType.id === currentValue)) {
        return row;
      }
      const resolved = hostTypeItems.find(
        (hostType) => hostType.id === currentValue || hostType.metadata?.name === currentValue,
      );
      if (resolved?.id && resolved.id !== currentValue) {
        changed = true;
        return { ...row, hostType: resolved.id };
      }
      return row;
    });
    if (changed) {
      void setFieldValue(name, nextRows, false);
    }
  }, [hostTypeItems, hostTypesLoading, name, rows, setFieldValue]);

  const addRow = () => {
    void helpers.setValue([...rows, createEmptyNodeSetRow()]);
  };

  const removeRow = (rowIndex: number) => {
    void helpers.setValue(rows.filter((_, index) => index !== rowIndex));
  };

  return (
    <Stack hasGutter>
      {hostTypesError ? (
        <StackItem>
          <Alert variant="danger" isInline title={t('Could not load host types')}>
            {getErrorMessage(hostTypesError)}
            <Button variant="link" isInline onClick={() => void refetchHostTypes()}>
              {t('catalogProvision.actions.retry')}
            </Button>
          </Alert>
        </StackItem>
      ) : null}
      {rows.length === 0 ? <StackItem>{t('No node sets added yet.')}</StackItem> : null}
      {rows.map((row, rowIndex) => (
        <StackItem key={row.rowId}>
          <ProvisioningFieldGroup
            title={t('Node set {{number}}', { number: rowIndex + 1 })}
            titleId={`cluster-node-set-group-${row.rowId}`}
            actions={
              !isDisabled && rowIndex > 0 ? (
                <Button
                  variant="plain"
                  aria-label={t('Remove node set')}
                  onClick={() => removeRow(rowIndex)}
                  icon={<MinusCircleIcon />}
                />
              ) : undefined
            }
          >
            <NameField
              name={`${name}.${rowIndex}.name`}
              fieldId={`cluster-node-set-name-${row.rowId}`}
              isDisabled={isDisabled}
            />
            <SelectField
              name={`${name}.${rowIndex}.hostType`}
              label={t('Host type')}
              fieldId={`cluster-host-type-${row.rowId}`}
              options={hostTypeOptions}
              isRequired
              isLoading={hostTypesLoading}
              placeholder={t('Select host type')}
              isDisabled={isDisabled}
            />
            <ClusterPoolSizeField
              name={`${name}.${rowIndex}.size`}
              fieldId={`cluster-node-set-size-${rowIndex}`}
              isRequired
              isDisabled={isDisabled}
            />
          </ProvisioningFieldGroup>
        </StackItem>
      ))}
      <StackItem>
        <ActionGroup>
          <Button
            variant="link"
            icon={<PlusCircleIcon />}
            onClick={addRow}
            isDisabled={isDisabled || hostTypesLoading}
          >
            {t('Add node set')}
          </Button>
        </ActionGroup>
      </StackItem>
    </Stack>
  );
};

export default ClusterNodeSetsArrayField;
