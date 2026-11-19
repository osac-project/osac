import TextAreaField from './base/TextAreaField';
import { useTranslation } from '../../hooks/useTranslation';

interface DescriptionFieldProps {
  name?: string;
  fieldId?: string;
  isDisabled?: boolean;
}

const DESCRIPTION_FIELD_ROWS = 3;

const DescriptionField = ({
  name = 'description',
  fieldId = 'description',
  isDisabled,
}: DescriptionFieldProps) => {
  const { t } = useTranslation();

  return (
    <TextAreaField
      name={name}
      label={t('Description')}
      fieldId={fieldId}
      rows={DESCRIPTION_FIELD_ROWS}
      isDisabled={isDisabled}
    />
  );
};

export default DescriptionField;
