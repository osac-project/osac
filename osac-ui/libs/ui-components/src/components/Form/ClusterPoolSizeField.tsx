import { InputField } from './InputField';
import { useTranslation } from '../../hooks/useTranslation';
import type { ProvisioningFieldControlProps } from '../ProvisioningWizard/fields/provisioningFieldValue';

interface ClusterPoolSizeFieldProps extends ProvisioningFieldControlProps {
  fieldId: string;
  isRequired?: boolean;
}

const ClusterPoolSizeField = ({
  name = '',
  fieldId,
  isRequired = false,
  isDisabled = false,
}: ClusterPoolSizeFieldProps) => {
  const { t } = useTranslation();

  return (
    <InputField
      name={name}
      label={t('Nodes')}
      fieldId={fieldId}
      isRequired={isRequired}
      isDisabled={isDisabled}
      type="number"
    />
  );
};

export default ClusterPoolSizeField;
