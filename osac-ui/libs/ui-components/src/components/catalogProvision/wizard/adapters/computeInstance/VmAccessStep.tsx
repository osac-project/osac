import { useFormikContext } from 'formik';

import { type ComputeInstanceCatalogItem, Secret, SecretType } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import SecretSelectionField from '@osac/ui-components/components/Form/SecretSelectionField';

import { ComputeInstanceWizardValues, VM_SSH_KEY_FORM_PATH } from './fields';
import { useTranslation } from '../../../../../hooks/useTranslation';
import OsacForm from '../../../../Form/OsacForm';
import UserDataField from '../../fields/UserDataField';

interface VmAccessStepProps {
  catalogItem: ComputeInstanceCatalogItem | null;
}

const VmAccessStep = ({ catalogItem }: VmAccessStepProps) => {
  const { t } = useTranslation();
  const isSshKeyLocked = catalogItem?.fields?.sshKey?.behavior.case === 'locked';
  const { values } = useFormikContext<ComputeInstanceWizardValues>();

  if (!catalogItem) {
    return null;
  }

  return (
    <OsacForm>
      <SecretSelectionField
        filter={cel<Secret>((filter) =>
          filter.and(
            filter.field('metadata.project').equals(values.metadata.project),
            filter.field('type').equals(SecretType.SSH_PUBLIC_KEY),
          ),
        )}
        label={t('SSH public key')}
        name={VM_SSH_KEY_FORM_PATH}
        isDisabled={isSshKeyLocked}
      />
      <UserDataField catalogItem={catalogItem} name="spec.userData" wirePath="spec.user_data" />
    </OsacForm>
  );
};

export default VmAccessStep;
