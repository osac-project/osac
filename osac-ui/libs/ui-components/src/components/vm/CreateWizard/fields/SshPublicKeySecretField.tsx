import { Secret, SecretType } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import SecretSelectionField from '@osac/ui-components/components/Form/SecretSelectionField';

import { useTranslation } from '../../../../hooks/useTranslation';
import type { ProvisioningFieldControlProps } from '../../../ProvisioningWizard/fields/provisioningFieldValue';

interface SshPublicKeySecretFieldProps extends ProvisioningFieldControlProps {
  project: string;
}

const SshPublicKeySecretField = ({
  name,
  project,
  isDisabled = false,
}: SshPublicKeySecretFieldProps) => {
  const { t } = useTranslation();

  return (
    <SecretSelectionField
      filter={cel<Secret>((filter) =>
        filter.and(
          filter.field('metadata.project').equals(project),
          filter.field('type').equals(SecretType.SSH_PUBLIC_KEY),
        ),
      )}
      label={t('SSH public key')}
      name={name}
      fieldId={name.replace(/\./g, '-')}
      isDisabled={isDisabled}
    />
  );
};

export default SshPublicKeySecretField;
