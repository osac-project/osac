import TextAreaField from './base/TextAreaField';
import { useTranslation } from '../../hooks/useTranslation';

interface DescriptionFieldProps {
  name: string;
  isDisabled?: boolean;
}

const DESCRIPTION_FIELD_ROWS = 3;

const DescriptionField = ({ name, isDisabled }: DescriptionFieldProps) => {
  const { t } = useTranslation();

  return (
    <TextAreaField
      name={name}
      label={t('Description')}
      fieldId={name}
      rows={DESCRIPTION_FIELD_ROWS}
      isDisabled={isDisabled}
    />
  );
};

export default DescriptionField;
