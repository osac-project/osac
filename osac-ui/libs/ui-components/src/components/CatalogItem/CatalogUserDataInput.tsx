import type { ReactNode } from 'react';

import { useTranslation } from '../../hooks/useTranslation';
import { InputField } from '../Form/InputField';

interface CatalogUserDataInputProps {
  valueName: string;
  isDisabled: boolean;
  isRequired: boolean;
  labelInfo: ReactNode;
}

const CatalogUserDataInput = ({
  valueName,
  isDisabled,
  isRequired,
  labelInfo,
}: CatalogUserDataInputProps) => {
  const { t } = useTranslation();

  // Secret-backed values remain hidden while the authoring UI offers an optional inline override.
  return (
    <InputField
      name={valueName}
      label={t('User data')}
      fieldId="catalog-user-data"
      multiline
      helperText={t('Optional cloud-init user data (max 64 KB).')}
      isDisabled={isDisabled}
      isRequired={isRequired}
      labelInfo={labelInfo}
    />
  );
};

export default CatalogUserDataInput;
