import {
  ActionGroup,
  Alert,
  Button,
  FormFieldGroup,
  FormFieldGroupHeader,
  Stack,
  StackItem,
} from '@patternfly/react-core';
import MinusCircleIcon from '@patternfly/react-icons/dist/esm/icons/minus-circle-icon';
import PlusCircleIcon from '@patternfly/react-icons/dist/esm/icons/plus-circle-icon';
import { useFormikContext } from 'formik';

import { BareMetalInstanceTypes } from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';

import type { ClusterWizardValues } from './fields';
import { createEmptyNodeSetRow } from './fields';
import { useTranslation } from '../../../../../hooks/useTranslation';
import { getErrorMessage } from '../../../../../utils/error';
import { SelectField } from '../../../../Form/SelectField';
import ClusterPoolSizeField from '../../fields/ClusterPoolSizeField';
import NameField from '../../fields/NameField';

const ClusterNodeSetsArrayField = () => {
  const { t } = useTranslation();
  const { values, setFieldValue } = useFormikContext<ClusterWizardValues>();
  const {
    data: hostTypes,
    isLoading: hostTypesLoading,
    error: hostTypesError,
    refetch: refetchHostTypes,
  } = useListResource(BareMetalInstanceTypes);

  const hostTypeOptions = (hostTypes?.items || []).map((hostType) => ({
    value: hostType.id,
    label: hostType.metadata?.name || '',
  }));

  const addRow = () => {
    void setFieldValue('spec.nodeSetRows', [...values.spec.nodeSetRows, createEmptyNodeSetRow()]);
  };

  const removeRow = (rowIndex: number) => {
    void setFieldValue(
      'spec.nodeSetRows',
      values.spec.nodeSetRows.filter((_, index) => index !== rowIndex),
    );
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
      {values.spec.nodeSetRows.length === 0 ? (
        <StackItem>{t('No node sets added yet.')}</StackItem>
      ) : null}
      {values.spec.nodeSetRows.map((row, rowIndex) => (
        <StackItem key={row.rowId}>
          <FormFieldGroup
            header={
              <FormFieldGroupHeader
                titleText={{
                  text: t('Node set {{number}}', { number: rowIndex + 1 }),
                  id: `cluster-node-set-group-${row.rowId}`,
                }}
                actions={
                  rowIndex > 0 ? (
                    <Button
                      variant="plain"
                      aria-label={t('Remove node set')}
                      onClick={() => removeRow(rowIndex)}
                      icon={<MinusCircleIcon />}
                    />
                  ) : undefined
                }
              />
            }
          >
            <NameField
              name={`spec.nodeSetRows.${rowIndex}.name`}
              fieldId={`cluster-node-set-name-${row.rowId}`}
            />
            <SelectField
              name={`spec.nodeSetRows.${rowIndex}.hostType`}
              label={t('Host type')}
              fieldId={`cluster-host-type-${row.rowId}`}
              options={hostTypeOptions}
              isRequired
              isLoading={hostTypesLoading}
              placeholder={t('Select host type')}
            />
            <ClusterPoolSizeField rowIndex={rowIndex} isRequired />
          </FormFieldGroup>
        </StackItem>
      ))}
      <StackItem>
        <ActionGroup>
          <Button
            variant="link"
            icon={<PlusCircleIcon />}
            onClick={addRow}
            isDisabled={hostTypesLoading}
          >
            {t('Add node set')}
          </Button>
        </ActionGroup>
      </StackItem>
    </Stack>
  );
};

export default ClusterNodeSetsArrayField;
