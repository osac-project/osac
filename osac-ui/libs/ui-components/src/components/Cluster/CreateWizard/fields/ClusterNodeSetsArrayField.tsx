import { useEffect } from 'react';
import { ActionGroup, Button, Stack, StackItem } from '@patternfly/react-core';
import MinusCircleIcon from '@patternfly/react-icons/dist/esm/icons/minus-circle-icon';
import PlusCircleIcon from '@patternfly/react-icons/dist/esm/icons/plus-circle-icon';
import { useField } from 'formik';

import { type BareMetalInstanceType, BareMetalInstanceTypes } from '@osac/types';

import ClusterPoolSizeField from './ClusterPoolSizeField';
import { useTranslation } from '../../../../hooks/useTranslation';
import { getVisibleFieldError } from '../../../Form/fieldError';
import { useShowFieldValidationErrors } from '../../../Form/FieldValidationContext';
import { FormFieldHelper } from '../../../Form/FormFieldHelper';
import NameField from '../../../Form/NameField';
import { ResourceSelectField } from '../../../Form/ResourceSelectField';
import ProvisioningFieldGroup from '../../../ProvisioningWizard/fields/ProvisioningFieldGroup';
import type { ProvisioningFieldControlProps } from '../../../ProvisioningWizard/fields/provisioningFieldValue';
import { type ClusterNodeSetRow, createEmptyNodeSetRow } from '../fields';

interface ClusterNodeSetsArrayFieldProps extends ProvisioningFieldControlProps {
  /** OSAC-6054: catalog policies may omit node sets; provisioning requires one row. */
  allowEmpty?: boolean;
  autoSelectSingleOption?: boolean;
}

const ClusterNodeSetsArrayField = ({
  name = '',
  isDisabled = false,
  isRequired = false,
  allowEmpty = false,
  autoSelectSingleOption = true,
}: ClusterNodeSetsArrayFieldProps) => {
  const { t } = useTranslation();
  const [field, meta, helpers] = useField<ClusterNodeSetRow[]>(name);
  const showValidationErrors = useShowFieldValidationErrors();
  const error = getVisibleFieldError(meta, showValidationErrors);
  const rows = field.value ?? [];

  useEffect(() => {
    if (isRequired && rows.length === 0) {
      void helpers.setValue([createEmptyNodeSetRow()]);
    }
  }, [helpers, isRequired, rows.length]);

  const addRow = () => {
    void helpers.setValue([...rows, createEmptyNodeSetRow()]);
  };

  const removeRow = (rowIndex: number) => {
    void helpers.setValue(rows.filter((_, index) => index !== rowIndex));
  };

  return (
    <Stack hasGutter>
      {error ? (
        <StackItem>
          <FormFieldHelper error={error} fieldId={`${name}-error`} />
        </StackItem>
      ) : null}
      {rows.length === 0 ? <StackItem>{t('No node sets added yet.')}</StackItem> : null}
      {rows.map((row, rowIndex) => (
        <StackItem key={row.rowId}>
          <ProvisioningFieldGroup
            title={t('Node set {{number}}', { number: rowIndex + 1 })}
            titleId={`cluster-node-set-group-${row.rowId}`}
            actions={
              !isDisabled && (allowEmpty || rowIndex > 0) ? (
                <Button
                  variant="plain"
                  aria-label={t('Remove node set')}
                  onClick={() => removeRow(rowIndex)}
                  isDisabled={isRequired && rows.length === 1}
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
            <ResourceSelectField<BareMetalInstanceType>
              name={`${name}.${rowIndex}.hostType`}
              label={t('Host type')}
              fieldId={`cluster-host-type-${row.rowId}`}
              service={BareMetalInstanceTypes}
              isRequired
              autoSelectSingleOption={autoSelectSingleOption}
              placeholder={t('Select host type')}
              loadErrorTitle={t('Could not load host types')}
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
          <Button variant="link" icon={<PlusCircleIcon />} onClick={addRow} isDisabled={isDisabled}>
            {t('Add node set')}
          </Button>
        </ActionGroup>
      </StackItem>
    </Stack>
  );
};

export default ClusterNodeSetsArrayField;
