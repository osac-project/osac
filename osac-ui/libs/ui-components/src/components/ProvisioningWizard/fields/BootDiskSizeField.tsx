import type { ProvisioningFieldControlProps } from './provisioningFieldValue';
import { useTranslation } from '../../../hooks/useTranslation';
import { InputField } from '../../Form/InputField';

interface BootDiskSizeFieldProps extends ProvisioningFieldControlProps {
  fieldId?: string;
}

const BootDiskSizeField = ({
  name = '',
  fieldId = 'vm-boot-disk-size',
  isDisabled = false,
}: BootDiskSizeFieldProps) => {
  const { t } = useTranslation();

  return (
    <InputField
      name={name}
      label={t('Boot disk')}
      fieldId={fieldId}
      type="number"
      isRequired
      isDisabled={isDisabled}
      helperText={t('Size in GiB')}
    />
  );
};

export default BootDiskSizeField;
