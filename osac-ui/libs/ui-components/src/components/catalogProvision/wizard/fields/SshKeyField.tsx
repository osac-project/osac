import { useField, useFormikContext } from 'formik';

import { CatalogItem } from '@osac/ui-components/components/catalog/catalogItemDisplay';

import { useTranslation } from '../../../../hooks/useTranslation';
import { InputField } from '../../../Form/InputField';
import {
  getCatalogFieldOverlay,
  hasCatalogFieldDefinition,
  readCatalogFieldDefinitions,
} from '../catalogOverlay';
import { trimSshPublicKey } from './credentialValidation';

interface SshKeyFieldProps {
  catalogItem: CatalogItem | null;
  wirePath: string;
  name: string;
}

const SshKeyField = ({ catalogItem, wirePath, name }: SshKeyFieldProps) => {
  const { t } = useTranslation();
  const { setFieldValue } = useFormikContext();
  const [field] = useField<string>(name);
  const definitions = readCatalogFieldDefinitions(catalogItem);
  const overlay = getCatalogFieldOverlay(wirePath, definitions, t('SSH public key'));
  const isRequired = hasCatalogFieldDefinition(wirePath, definitions);

  const handleBlur = () => {
    const trimmed = trimSshPublicKey(field.value ?? '');
    if (trimmed !== field.value) {
      void setFieldValue(name, trimmed);
    }
  };

  return (
    <InputField
      name={name}
      label={overlay.label}
      fieldId={name.replace(/\./g, '-')}
      isRequired={isRequired}
      isDisabled={!overlay.editable}
      multiline
      helperText={t(
        'Paste a public SSH key for remote access. Supported types: ssh-rsa, ssh-ed25519, and ecdsa-sha2-nistp256/384/521.',
      )}
      onBlur={handleBlur}
    />
  );
};

export default SshKeyField;
