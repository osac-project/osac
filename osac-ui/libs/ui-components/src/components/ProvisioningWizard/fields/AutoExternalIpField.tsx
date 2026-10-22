import type { ProvisioningFieldControlProps } from './provisioningFieldValue';
import { useTranslation } from '../../../hooks/useTranslation';
import { CheckboxField } from '../../Form/CheckboxField';

interface AutoExternalIpFieldProps extends ProvisioningFieldControlProps {
  helperText?: string;
}

const AutoExternalIpField = ({
  name,
  helperText,
  isDisabled = false,
}: AutoExternalIpFieldProps) => {
  const { t } = useTranslation();

  return (
    <CheckboxField
      name={name}
      label={t('Auto External IP Attachment')}
      fieldId={name}
      helperText={helperText}
      isDisabled={isDisabled}
    />
  );
};

export default AutoExternalIpField;
