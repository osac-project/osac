import type { ReactNode } from 'react';

import { useTranslation } from '../../../hooks/useTranslation';
import { CATALOG_PROVISION_MULTILINE_TEXTAREA } from '../../Form/constants';
import { InputField } from '../../Form/InputField';

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

  // Catalog items created through the CLI may reference a secret, but the authoring UI only
  // supports text input. Secret-backed values are intentionally ignored when editing.
  return (
    <InputField
      name={valueName}
      label={t('User data')}
      fieldId="catalog-user-data"
      multiline
      rows={CATALOG_PROVISION_MULTILINE_TEXTAREA.rows}
      resizeOrientation={CATALOG_PROVISION_MULTILINE_TEXTAREA.resizeOrientation}
      helperText={t('Optional cloud-init user data (max 64 KB).')}
      isDisabled={isDisabled}
      isRequired={isRequired}
      labelInfo={labelInfo}
    />
  );
};

export default CatalogUserDataInput;
