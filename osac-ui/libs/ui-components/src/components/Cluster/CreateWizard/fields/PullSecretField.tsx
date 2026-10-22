import { Secret, SecretType } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import SecretSelectionField from '@osac/ui-components/components/Form/SecretSelectionField';

import { useTranslation } from '../../../../hooks/useTranslation';
import type { ProvisioningFieldControlProps } from '../../../ProvisioningWizard/fields/provisioningFieldValue';

interface PullSecretFieldProps extends ProvisioningFieldControlProps {
  project: string;
}

const PullSecretField = ({ name, project, isDisabled = false }: PullSecretFieldProps) => {
  const { t } = useTranslation();

  return (
    <SecretSelectionField
      filter={cel<Secret>((filter) =>
        filter.and(
          filter.field('metadata.project').equals(project),
          filter.field('type').equals(SecretType.PULL_SECRET),
        ),
      )}
      label={t('Pull secret secret')}
      name={name}
      fieldId={name.replace(/\./g, '-')}
      isRequired
      isDisabled={isDisabled}
    />
  );
};

export default PullSecretField;
