import { useFormikContext } from 'formik';

import type { ClusterCatalogItem } from '@osac/types';
import ProjectField from '@osac/ui-components/components/Form/ProjectField';

import {
  CLUSTER_PULL_SECRET_FORM_PATH,
  CLUSTER_SSH_KEY_FORM_PATH,
  ClusterWizardValues,
} from './fields';
import NameField from '../../Form/NameField';
import OsacForm from '../../Form/OsacForm';
import SshKeyField from '../../Form/SshKeyField';
import ProvisioningField from '../../ProvisioningWizard/fields/ProvisioningField';
import PullSecretField from '../../ProvisioningWizard/fields/PullSecretField';

interface ClusterGeneralStepProps {
  catalogItem: ClusterCatalogItem | null;
}

const ClusterGeneralStep = (_props: ClusterGeneralStepProps) => {
  const { values, setFieldValue } = useFormikContext<ClusterWizardValues>();

  return (
    <OsacForm>
      <ProjectField
        onSelect={() => {
          if (!values.spec.pullSecretSecret.locked) {
            void setFieldValue(`${CLUSTER_PULL_SECRET_FORM_PATH}.value`, '');
          }
        }}
      />
      <NameField />
      <ProvisioningField name={CLUSTER_SSH_KEY_FORM_PATH}>
        {({ valueName, isDisabled }) => <SshKeyField name={valueName} isDisabled={isDisabled} />}
      </ProvisioningField>
      <ProvisioningField name={CLUSTER_PULL_SECRET_FORM_PATH}>
        {({ valueName, isDisabled }) => (
          <PullSecretField
            name={valueName}
            project={values.metadata.project}
            isDisabled={isDisabled}
          />
        )}
      </ProvisioningField>
    </OsacForm>
  );
};

export default ClusterGeneralStep;
