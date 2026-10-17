import type { ProvisioningFieldControlProps } from './provisioningFieldValue';
import { useTranslation } from '../../../hooks/useTranslation';
import { CheckboxField } from '../../Form/CheckboxField';

interface AutoExternalIpFieldProps extends ProvisioningFieldControlProps {
  fieldId?: string;
  helperText?: string;
}

const AutoExternalIpField = ({
  name = '',
  fieldId = 'auto-external-ip',
  helperText,
  isDisabled = false,
}: AutoExternalIpFieldProps) => {
  const { t } = useTranslation();

  return (
    <CheckboxField
      name={name}
      label={t('Auto External IP Attachment')}
      fieldId={fieldId}
      helperText={helperText}
      isDisabled={isDisabled}
    />
  );
};

export default AutoExternalIpField;
