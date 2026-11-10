import { InputField } from './InputField';
import { useTranslation } from '../../hooks/useTranslation';
import type { ProvisioningFieldControlProps } from '../ProvisioningWizard/fields/provisioningFieldValue';

const SshKeyField = ({ name = '', isDisabled = false }: ProvisioningFieldControlProps) => {
  const { t } = useTranslation();

  return (
    <InputField
      name={name}
      label={t('SSH public key')}
      fieldId={name.replace(/\./g, '-')}
      multiline
      isDisabled={isDisabled}
      helperText={t(
        'Paste a public SSH key for remote access. Supported types: ssh-rsa, ssh-ed25519, and ecdsa-sha2-nistp256/384/521.',
      )}
    />
  );
};

export default SshKeyField;
