import { useField, useFormikContext } from 'formik';

import { CATALOG_PROVISION_MULTILINE_TEXTAREA } from './constants';
import { InputField } from './InputField';
import { useTranslation } from '../../hooks/useTranslation';
import { trimSshPublicKey } from '../ProvisioningWizard/credentialValidation';
import type { ProvisioningFieldControlProps } from '../ProvisioningWizard/fields/provisioningFieldValue';

const SshKeyField = ({ name = '', isDisabled = false }: ProvisioningFieldControlProps) => {
  const { t } = useTranslation();
  const { setFieldValue } = useFormikContext();
  const [field] = useField<string>(name);
  const handleBlur = () => {
    const trimmed = trimSshPublicKey(field.value ?? '');
    if (trimmed !== field.value) {
      void setFieldValue(name, trimmed);
    }
  };

  return (
    <InputField
      name={name}
      label={t('SSH public key')}
      fieldId={name.replace(/\./g, '-')}
      multiline
      rows={CATALOG_PROVISION_MULTILINE_TEXTAREA.rows}
      resizeOrientation={CATALOG_PROVISION_MULTILINE_TEXTAREA.resizeOrientation}
      isDisabled={isDisabled}
      helperText={t(
        'Paste a public SSH key for remote access. Supported types: ssh-rsa, ssh-ed25519, and ecdsa-sha2-nistp256/384/521.',
      )}
      onBlur={handleBlur}
    />
  );
};

export default SshKeyField;
