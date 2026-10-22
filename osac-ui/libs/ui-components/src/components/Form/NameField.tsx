import { InputField } from './InputField';
import { useTranslation } from '../../hooks/useTranslation';

interface NameFieldProps {
  isDisabled?: boolean;
  name?: string;
  fieldId?: string;
}

const NameField = ({
  isDisabled,
  name = 'metadata.name',
  fieldId = 'metadata-name',
}: NameFieldProps) => {
  const { t } = useTranslation();

  return (
    <InputField
      name={name}
      label={t('Name')}
      fieldId={fieldId}
      isRequired
      helperText={t('Name must be a valid DNS label (RFC 1035).')}
      isDisabled={isDisabled}
    />
  );
};

export default NameField;
