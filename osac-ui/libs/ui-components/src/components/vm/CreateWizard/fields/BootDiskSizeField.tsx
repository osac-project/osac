import { useTranslation } from '../../../../hooks/useTranslation';
import { InputField } from '../../../Form/InputField';
import type { ProvisioningFieldControlProps } from '../../../ProvisioningWizard/fields/provisioningFieldValue';

interface BootDiskSizeFieldProps extends ProvisioningFieldControlProps {
  fieldId?: string;
}

const BootDiskSizeField = ({
  name,
  fieldId = 'vm-boot-disk-size',
  isDisabled = false,
  isRequired = true,
  labelInfo,
}: BootDiskSizeFieldProps) => {
  const { t } = useTranslation();

  return (
    <InputField
      name={name}
      label={t('Boot disk')}
      fieldId={fieldId}
      type="number"
      isRequired={isRequired}
      isDisabled={isDisabled}
      labelInfo={labelInfo}
      helperText={t('Size in GiB')}
    />
  );
};

export default BootDiskSizeField;
