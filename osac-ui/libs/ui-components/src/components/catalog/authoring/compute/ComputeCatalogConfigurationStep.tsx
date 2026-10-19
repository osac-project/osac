import { Content, Stack, StackItem, Title } from '@patternfly/react-core';

import { useTranslation } from '../../../../hooks/useTranslation';
import { InputField } from '../../../Form/InputField';
import OsacForm from '../../../Form/OsacForm';
import ComputeInstanceTypeField from '../../../ProvisioningWizard/fields/ComputeInstanceTypeField';
import DiskImageField from '../../../ProvisioningWizard/fields/DiskImageField';
import CatalogField from '../CatalogField';

const ComputeCatalogConfigurationStep = () => {
  const { t } = useTranslation();

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Configuration')}
        </Title>
      </StackItem>
      <StackItem>
        <Content component="p">
          {t(
            'Optionally set instance type defaults for this catalog item. Leave fields Not set if tenants should choose when creating a virtual machine.',
          )}
        </Content>
      </StackItem>
      <StackItem>
        <OsacForm>
          <CatalogField name="diskImage">
            {({ valueName, isDisabled, labelInfo }) => (
              <DiskImageField
                name={valueName}
                fieldId="compute-catalog-disk-image"
                isDisabled={isDisabled}
                isRequired={false}
                labelInfo={labelInfo}
              />
            )}
          </CatalogField>
          <CatalogField name="instanceType">
            {({ valueName, isDisabled, labelInfo }) => (
              <ComputeInstanceTypeField
                name={`${valueName}.id`}
                fieldId="compute-catalog-instance-type"
                isDisabled={isDisabled}
                isRequired={false}
                labelInfo={labelInfo}
              />
            )}
          </CatalogField>
          <CatalogField name="userData">
            {({ valueName, isDisabled, labelInfo }) => (
              <InputField
                name={valueName}
                label={t('User data')}
                fieldId="compute-catalog-user-data"
                multiline
                isDisabled={isDisabled}
                labelInfo={labelInfo}
              />
            )}
          </CatalogField>
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default ComputeCatalogConfigurationStep;
