import { useTranslation } from '../../../../hooks/useTranslation';
import { InputField } from '../../../Form/InputField';
import type { ProvisioningFieldControlProps } from '../../../ProvisioningWizard/fields/provisioningFieldValue';

interface ClusterServiceCidrFieldProps extends ProvisioningFieldControlProps {
  fieldId?: string;
}

const ClusterServiceCidrField = ({
  name,
  fieldId = 'cluster-service-cidr',
  isDisabled = false,
}: ClusterServiceCidrFieldProps) => {
  const { t } = useTranslation();

  return (
    <InputField
      name={name}
      label={t('Service CIDR')}
      fieldId={fieldId}
      isDisabled={isDisabled}
      helperText={t('Use IPv4 CIDR notation (for example 172.30.0.0/16).')}
    />
  );
};

export default ClusterServiceCidrField;
