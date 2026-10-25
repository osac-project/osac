import { Content, Stack, StackItem, Title } from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import type { ComputeCatalogItemValues } from './values';
import { useTranslation } from '../../../../hooks/useTranslation';
import OsacForm from '../../../Form/OsacForm';
import UserDataField from '../../../Form/UserDataField';
import DiskImageField from '../../../ProvisioningWizard/fields/DiskImageField';
import ComputeInstanceTypeField from '../../../vm/CreateWizard/fields/ComputeInstanceTypeField';
import CatalogField from '../CatalogField';

const ComputeCatalogConfigurationStep = () => {
  const { t } = useTranslation();
  const { values } = useFormikContext<ComputeCatalogItemValues>();

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Compute configuration')}
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
            {({ valueName, isDisabled, isRequired, labelInfo }) => (
              <DiskImageField
                name={valueName}
                fieldId="compute-catalog-disk-image"
                isDisabled={isDisabled}
                isRequired={isRequired}
                labelInfo={labelInfo}
              />
            )}
          </CatalogField>
          <CatalogField name="instanceType">
            {({ valueName, isDisabled, isRequired, labelInfo }) => (
              <ComputeInstanceTypeField
                name={`${valueName}.id`}
                fieldId="compute-catalog-instance-type"
                isDisabled={isDisabled}
                isRequired={isRequired}
                labelInfo={labelInfo}
              />
            )}
          </CatalogField>
          <CatalogField name="userData">
            {({ valueName, isDisabled, isRequired, labelInfo }) => (
              <UserDataField
                sourceName="userDataSource.value"
                secretRefName="userDataSecret.value"
                inlineName={valueName}
                projectName={values.metadata.project}
                isDisabled={isDisabled}
                isRequired={isRequired}
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
