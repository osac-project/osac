import type { ReactNode } from 'react';
import { useField } from 'formik';

import { useTranslation } from '../../../hooks/useTranslation';
import { CATALOG_PROVISION_MULTILINE_TEXTAREA } from '../../Form/constants';
import { InputField } from '../../Form/InputField';
import type {
  ProvisioningFieldValue,
  UserDataSourceKind,
} from '../../ProvisioningWizard/fields/provisioningFieldValue';

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
  const [sourceField] = useField<ProvisioningFieldValue<UserDataSourceKind>>('userDataSource');
  const isSecret = sourceField.value.value === 'secret';

  return (
    <InputField
      name={isSecret ? 'userDataSecret.value' : valueName}
      label={t('User data')}
      fieldId="catalog-user-data"
      multiline
      rows={CATALOG_PROVISION_MULTILINE_TEXTAREA.rows}
      resizeOrientation={CATALOG_PROVISION_MULTILINE_TEXTAREA.resizeOrientation}
      helperText={t('Optional cloud-init user data (max 64 KB).')}
      isDisabled={isDisabled || isSecret}
      isRequired={isRequired}
      labelInfo={labelInfo}
    />
  );
};

export default CatalogUserDataInput;
