import {
  ActionGroup,
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

import type { ClusterWizardValues } from './fields';
import { createEmptyNodeSetRow } from './fields';
import { useTranslation } from '../../../../../hooks/useTranslation';
import { ResourceSelectField } from '../../../../Form/ResourceSelectField';
import ClusterPoolSizeField from '../../fields/ClusterPoolSizeField';
import NameField from '../../fields/NameField';

const ClusterNodeSetsArrayField = () => {
  const { t } = useTranslation();
  const { values, setFieldValue } = useFormikContext<ClusterWizardValues>();

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
            <ResourceSelectField
              name={`spec.nodeSetRows.${rowIndex}.baremetalInstanceType`}
              label={t('Instance type')}
              fieldId={`cluster-instance-type-${row.rowId}`}
              service={BareMetalInstanceTypes}
              isRequired
              placeholder={t('Select instance type')}
              loadErrorTitle={t('Could not load instance types')}
            />
            <ClusterPoolSizeField rowIndex={rowIndex} isRequired />
          </FormFieldGroup>
        </StackItem>
      ))}
      <StackItem>
        <ActionGroup>
          <Button variant="link" icon={<PlusCircleIcon />} onClick={addRow}>
            {t('Add node set')}
          </Button>
        </ActionGroup>
      </StackItem>
    </Stack>
  );
};

export default ClusterNodeSetsArrayField;
