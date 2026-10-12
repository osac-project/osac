import { useFormikContext } from 'formik';

import { type ComputeInstanceCatalogItem } from '@osac/types';
import ProjectField from '@osac/ui-components/components/Form/ProjectField';

import { ComputeInstanceWizardValues, VM_SSH_KEY_FORM_PATH } from './fields';
import OsacForm from '../../../../Form/OsacForm';
import NameField from '../../fields/NameField';

interface VmGeneralStepProps {
  catalogItem: ComputeInstanceCatalogItem | null;
}

const VmGeneralStep = (_props: VmGeneralStepProps) => {
  const { setFieldValue } = useFormikContext<ComputeInstanceWizardValues>();

  return (
    <OsacForm>
      <ProjectField onSelect={() => setFieldValue(VM_SSH_KEY_FORM_PATH, '')} />
      <NameField />
    </OsacForm>
  );
};

export default VmGeneralStep;
