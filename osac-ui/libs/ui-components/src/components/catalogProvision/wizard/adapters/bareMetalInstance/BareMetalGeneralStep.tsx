import { useFormikContext } from 'formik';

import type { BareMetalInstanceCatalogItem } from '@osac/types';
import ProjectField from '@osac/ui-components/components/Form/ProjectField';

import {
  BM_SSH_KEY_FORM_PATH,
  BM_SSH_KEY_WIRE_PATH,
  BM_USER_DATA_SECRET_FORM_PATH,
  BareMetalInstanceWizardValues,
} from './fields';
import OsacForm from '../../../../Form/OsacForm';
import NameField from '../../fields/NameField';
import SshKeyField from '../../fields/SshKeyField';

interface BareMetalGeneralStepProps {
  catalogItem: BareMetalInstanceCatalogItem | null;
}

const BareMetalGeneralStep = ({ catalogItem }: BareMetalGeneralStepProps) => {
  const { setFieldValue } = useFormikContext<BareMetalInstanceWizardValues>();

  return (
    <OsacForm>
      <ProjectField
        onSelect={() => {
          void setFieldValue(BM_USER_DATA_SECRET_FORM_PATH, '');
          void setFieldValue(BM_SSH_KEY_FORM_PATH, '');
        }}
      />
      <NameField />
      <SshKeyField
        catalogItem={catalogItem}
        wirePath={BM_SSH_KEY_WIRE_PATH}
        name={BM_SSH_KEY_FORM_PATH}
      />
    </OsacForm>
  );
};

export default BareMetalGeneralStep;
