import { FormSection, Stack, StackItem } from '@patternfly/react-core';

import { type ClusterCatalogItem } from '@osac/types';

import ClusterNodeSetsArrayField from './ClusterNodeSetsArrayField';
import { useTranslation } from '../../../hooks/useTranslation';
import OsacForm from '../../Form/OsacForm';
import ClusterVersionField from '../../ProvisioningWizard/fields/ClusterVersionField';
import ProvisioningField from '../../ProvisioningWizard/fields/ProvisioningField';

interface Props {
  catalogItem: ClusterCatalogItem | null;
}

const ClusterConfigurationStep = ({ catalogItem }: Props) => {
  const { t } = useTranslation();

  if (!catalogItem) {
    return null;
  }

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
