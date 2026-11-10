import { type InstanceType, InstanceTypeState, InstanceTypes } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import { formatInstanceTypeOptionLabel } from '@osac/ui-components/components/vm/utils';

import { useTranslation } from '../../../../hooks/useTranslation';
import { ResourceSelectField } from '../../../Form/ResourceSelectField';
import type { ProvisioningFieldControlProps } from '../../../ProvisioningWizard/fields/provisioningFieldValue';

interface ComputeInstanceTypeFieldProps extends ProvisioningFieldControlProps {
  fieldId?: string;
  autoSelectSingleOption?: boolean;
}

const ComputeInstanceTypeField = ({
  name,
  fieldId = 'vm-instance-type',
  isDisabled = false,
  isRequired = true,
  labelInfo,
  autoSelectSingleOption = true,
}: ComputeInstanceTypeFieldProps) => {
  const { t } = useTranslation();

  return (
    <ResourceSelectField<InstanceType>
      name={name}
      label={t('catalogProvision.vm.fields.instanceType')}
      fieldId={fieldId}
      service={InstanceTypes}
      request={{
        filter: cel<InstanceType>((filter) =>
          filter.field('spec.state').equals(InstanceTypeState.ACTIVE),
        ),
      }}
      isRequired={isRequired}
      isDisabled={isDisabled}
      labelInfo={labelInfo}
      autoSelectSingleOption={autoSelectSingleOption}
      placeholder={t('catalogProvision.vm.placeholders.selectInstanceType')}
      loadErrorTitle={t('catalogProvision.instanceTypes.loadError')}
      formatOptionLabel={(instanceType) =>
        formatInstanceTypeOptionLabel(
          instanceType,
          t('catalogProvision.instanceTypes.deprecatedSuffix'),
        )
      }
    />
  );
};

export default ComputeInstanceTypeField;
