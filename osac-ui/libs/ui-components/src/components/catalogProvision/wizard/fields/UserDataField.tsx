import { CatalogItem } from '@osac/ui-components/components/catalog/catalogItemDisplay';
import { InputField } from '@osac/ui-components/components/Form/InputField';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import {
  getCatalogFieldOverlay,
  hasCatalogFieldDefinition,
  readCatalogFieldDefinitions,
} from '../catalogOverlay';
import { CATALOG_PROVISION_MULTILINE_TEXTAREA } from '../constants';

type UserDataFieldProps = {
  catalogItem: CatalogItem | null;
  wirePath: string;
  name: string;
};

const UserDataField = ({ catalogItem, wirePath, name }: UserDataFieldProps) => {
  const { t } = useTranslation();

  const definitions = readCatalogFieldDefinitions(catalogItem);
  const overlay = getCatalogFieldOverlay(wirePath, definitions, t('Cloud-init'));
  const isRequired = hasCatalogFieldDefinition(wirePath, definitions);
  return (
    <InputField
      name={name}
      label={overlay.label}
      fieldId="bm-user-data"
      multiline
      rows={CATALOG_PROVISION_MULTILINE_TEXTAREA.rows}
      resizeOrientation={CATALOG_PROVISION_MULTILINE_TEXTAREA.resizeOrientation}
      helperText={t('Optional cloud-init user data (max 64 KB).')}
      isDisabled={!overlay.editable}
      isRequired={isRequired}
    />
  );
};

export default UserDataField;
