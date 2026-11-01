import { Divider, Stack, StackItem, Title } from '@patternfly/react-core';

import { useTranslation } from '../../../../hooks/useTranslation';
import OsacForm from '../../../Form/OsacForm';
import DiskImageField from '../../../ProvisioningWizard/fields/DiskImageField';
import ComputeInstanceTypeField from '../../../vm/CreateWizard/fields/ComputeInstanceTypeField';
import CatalogField from '../CatalogField';
import CatalogFieldPolicyDescription from '../CatalogFieldPolicyDescription';

const VMCatalogConfigurationStep = () => {
  const { t } = useTranslation();

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Compute configuration')}
        </Title>
      </StackItem>
      <StackItem>
        <CatalogFieldPolicyDescription
          description={t(
            'Configure the disk image and instance type for virtual machines created from this catalog item.',
          )}
        />
      </StackItem>
      <StackItem>
        <OsacForm>
          <CatalogField name="diskImage">
            {({ valueName, isDisabled, isRequired, labelInfo }) => (
              <DiskImageField
                name={valueName}
                guestOsFamilyName="guestOsFamily"
                fieldId="compute-catalog-disk-image"
                isDisabled={isDisabled}
                isRequired={isRequired}
                labelInfo={labelInfo}
                autoSelectSingleOption={false}
              />
            )}
          </CatalogField>
          <Divider />
          <CatalogField name="instanceType">
            {({ valueName, isDisabled, isRequired, labelInfo }) => (
              <ComputeInstanceTypeField
                name={valueName}
                fieldId="compute-catalog-instance-type"
                isDisabled={isDisabled}
                isRequired={isRequired}
                labelInfo={labelInfo}
                autoSelectSingleOption={false}
              />
            )}
          </CatalogField>
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default VMCatalogConfigurationStep;
