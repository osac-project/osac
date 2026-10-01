import { useMemo } from 'react';
import { Alert, Button, Stack, StackItem } from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import { ComputeInstanceCatalogItem } from '@osac/types';
import { formatInstanceTypeOptionLabel } from '@osac/ui-components/components/vm/utils';

import {
  ComputeInstanceWizardValues,
  VM_USER_DATA_FORM_PATH,
  VM_USER_DATA_SECRET_FORM_PATH,
  VM_USER_DATA_SOURCE_FORM_PATH,
} from './fields';
import {
  INSTANCE_TYPE_ACTIVE_LIST_FILTER,
  useInstanceTypes,
} from '../../../../../api/v1/instance-types';
import { useTranslation } from '../../../../../hooks/useTranslation';
import OsacForm from '../../../../Form/OsacForm';
import { SelectField } from '../../../../Form/SelectField';
import UserDataField from '../../fields/UserDataField';

interface Props {
  catalogItem: ComputeInstanceCatalogItem | null;
}

export const VmConfigurationStep = ({ catalogItem }: Props) => {
  const { t } = useTranslation();
  const { values } = useFormikContext<ComputeInstanceWizardValues>();

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

  if (!catalogItem) {
    return null;
  }

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
        <OsacForm>
          <SelectField
            name="spec.instanceType"
            label={t('catalogProvision.vm.fields.instanceType')}
            fieldId="vm-instance-type"
            isRequired
            autoSelectSingleOption
            isLoading={instanceTypesLoading}
            placeholder={t('catalogProvision.vm.placeholders.selectInstanceType')}
            options={instanceTypeOptions}
          />
          <UserDataField
            sourceName={VM_USER_DATA_SOURCE_FORM_PATH}
            secretRefName={VM_USER_DATA_SECRET_FORM_PATH}
            currentSource={values.spec.userDataSource}
            inlineName={VM_USER_DATA_FORM_PATH}
            projectName={values.metadata.project}
          />
        </OsacForm>
      </StackItem>
    </Stack>
  );
};
