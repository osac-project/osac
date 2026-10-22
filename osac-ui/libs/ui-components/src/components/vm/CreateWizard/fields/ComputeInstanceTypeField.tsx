import { useEffect, useMemo } from 'react';
import { Alert, Button, Stack, StackItem } from '@patternfly/react-core';
import { useField, useFormikContext } from 'formik';

import { formatInstanceTypeOptionLabel } from '@osac/ui-components/components/vm/utils';

import {
  INSTANCE_TYPE_ACTIVE_LIST_FILTER,
  useInstanceTypes,
} from '../../../../api/v1/instance-types';
import { useTranslation } from '../../../../hooks/useTranslation';
import { SelectField } from '../../../Form/SelectField';
import type { ProvisioningFieldControlProps } from '../../../ProvisioningWizard/fields/provisioningFieldValue';

interface ComputeInstanceTypeFieldProps extends ProvisioningFieldControlProps {
  fieldId?: string;
}

const ComputeInstanceTypeField = ({
  name,
  fieldId = 'vm-instance-type',
  isDisabled = false,
}: ComputeInstanceTypeFieldProps) => {
  const { t } = useTranslation();
  const { setFieldValue } = useFormikContext();
  const [field] = useField<string>(name);
  const {
    data: instanceTypes = [],
    isPending: instanceTypesLoading,
    isError: instanceTypesError,
    refetch: refetchInstanceTypes,
  } = useInstanceTypes({ filter: INSTANCE_TYPE_ACTIVE_LIST_FILTER });

  const instanceTypeOptions = useMemo(
    () =>
      instanceTypes.map((instanceType) => ({
        value: instanceType.id,
        label: formatInstanceTypeOptionLabel(
          instanceType,
          t('catalogProvision.instanceTypes.deprecatedSuffix'),
        ),
      })),
    [instanceTypes, t],
  );

  useEffect(() => {
    const currentValue = field.value?.trim() ?? '';
    if (instanceTypesLoading || !currentValue) {
      return;
    }
    if (instanceTypeOptions.some((option) => option.value === currentValue)) {
      return;
    }
    const resolved = instanceTypes.find(
      (instanceType) =>
        instanceType.id === currentValue || instanceType.metadata?.name === currentValue,
    );
    if (resolved && resolved.id !== currentValue) {
      void setFieldValue(name, resolved.id, false);
    }
  }, [field.value, instanceTypeOptions, instanceTypes, instanceTypesLoading, name, setFieldValue]);

  return (
    <Stack hasGutter>
      {instanceTypesError ? (
        <StackItem>
          <Alert variant="danger" isInline title={t('catalogProvision.instanceTypes.loadError')}>
            <Button variant="link" isInline onClick={() => void refetchInstanceTypes()}>
              {t('catalogProvision.actions.retry')}
            </Button>
          </Alert>
        </StackItem>
      ) : null}
      <StackItem>
        <SelectField
          name={name}
          label={t('catalogProvision.vm.fields.instanceType')}
          fieldId={fieldId}
          isRequired
          autoSelectSingleOption
          isDisabled={isDisabled}
          isLoading={instanceTypesLoading}
          placeholder={t('catalogProvision.vm.placeholders.selectInstanceType')}
          options={instanceTypeOptions}
        />
      </StackItem>
    </Stack>
  );
};

export default ComputeInstanceTypeField;
