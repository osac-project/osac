import { type ComputeInstanceCatalogItem, Secret, SecretType } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import ProjectField from '@osac/ui-components/components/Form/ProjectField';
import SecretSelectionField from '@osac/ui-components/components/Form/SecretSelectionField';

import { VM_SSH_KEY_FORM_PATH } from './fields';
import { useTranslation } from '../../../../../hooks/useTranslation';
import OsacForm from '../../../../Form/OsacForm';
import NameField from '../../fields/NameField';

interface VmGeneralStepProps {
  catalogItem: ComputeInstanceCatalogItem | null;
}

const VmGeneralStep = ({ catalogItem }: VmGeneralStepProps) => {
  const { t } = useTranslation();
  const isSshKeyLocked = catalogItem?.fields?.sshKey?.behavior.case === 'locked';

  return (
    <OsacForm>
      <ProjectField />
      <NameField />
      <SecretSelectionField
        filter={cel<Secret>((filter) => filter.field('type').equals(SecretType.SSH_PUBLIC_KEY))}
        label={t('SSH public key')}
        name={VM_SSH_KEY_FORM_PATH}
        isDisabled={isSshKeyLocked}
        allowEmptySelection={!isSshKeyLocked}
      />
    </OsacForm>
  );
};

export default VmGeneralStep;
