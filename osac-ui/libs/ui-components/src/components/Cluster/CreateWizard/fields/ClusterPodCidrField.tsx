import { useTranslation } from '../../../../hooks/useTranslation';
import TextField from '../../../Form/base/TextField';
import type { ProvisioningFieldControlProps } from '../../../ProvisioningWizard/fields/provisioningFieldValue';

interface ClusterPodCidrFieldProps extends ProvisioningFieldControlProps {
  fieldId?: string;
}

const ClusterPodCidrField = ({
  name,
  fieldId = 'cluster-pod-cidr',
  isDisabled = false,
  isRequired = false,
  labelInfo,
}: ClusterPodCidrFieldProps) => {
  const { t } = useTranslation();

  return (
    <TextField
      name={name}
      label={t('Pod CIDR')}
      fieldId={fieldId}
      isDisabled={isDisabled}
      isRequired={isRequired}
      labelInfo={labelInfo}
      helperText={t('Use IPv4 CIDR notation (for example 10.128.0.0/14).')}
    />
  );
};

export default ClusterPodCidrField;
