import { FormSection, Stack, StackItem } from '@patternfly/react-core';

import ClusterNodeSetsArrayField from './ClusterNodeSetsArrayField';
import ClusterVersionField from './fields/ClusterVersionField';
import { useTranslation } from '../../../hooks/useTranslation';
import OsacForm from '../../Form/OsacForm';
import ProvisioningField from '../../ProvisioningWizard/fields/ProvisioningField';

const ClusterConfigurationStep = () => {
  const { t } = useTranslation();

  return (
    <Stack hasGutter>
      <StackItem>
        <OsacForm>
          <ProvisioningField name="spec.versionName">
            {({ valueName, isDisabled }) => (
              <ClusterVersionField name={valueName} isDisabled={isDisabled} />
            )}
          </ProvisioningField>
          <FormSection title={t('Node Sets')} titleElement="h2">
            <ProvisioningField name="spec.nodeSetRows">
              {({ valueName, isDisabled }) => (
                <ClusterNodeSetsArrayField name={valueName} isDisabled={isDisabled} />
              )}
            </ProvisioningField>
          </FormSection>
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default ClusterConfigurationStep;
