import { useTranslation } from '../../../../hooks/useTranslation';
import { InputField } from '../../../Form/InputField';
import type { ProvisioningFieldControlProps } from '../../../ProvisioningWizard/fields/provisioningFieldValue';

interface ClusterPodCidrFieldProps extends ProvisioningFieldControlProps {
  fieldId?: string;
}

const ClusterPodCidrField = ({
  name,
  fieldId = 'cluster-pod-cidr',
  isDisabled = false,
}: ClusterPodCidrFieldProps) => {
  const { t } = useTranslation();

  return (
    <InputField
      name={name}
      label={t('Pod CIDR')}
      fieldId={fieldId}
      isDisabled={isDisabled}
      helperText={t('Use IPv4 CIDR notation (for example 10.128.0.0/14).')}
    />
  );
};

export default ClusterPodCidrField;
