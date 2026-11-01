import { Stack, StackItem, Title } from '@patternfly/react-core';

import { useTranslation } from '../../../../hooks/useTranslation';
import BareMetalInstanceTypeField from '../../../BareMetalInstance/CreateWizard/fields/BareMetalInstanceTypeField';
import OsacForm from '../../../Form/OsacForm';
import DiskImageField from '../../../ProvisioningWizard/fields/DiskImageField';
import CatalogField from '../CatalogField';
import CatalogFieldPolicyDescription from '../CatalogFieldPolicyDescription';
import CatalogUserDataInput from '../CatalogUserDataInput';

const BareMetalCatalogConfigurationStep = () => {
  const { t } = useTranslation();

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Configuration')}
        </Title>
      </StackItem>
      <StackItem>
        <CatalogFieldPolicyDescription
          description={t(
            'Configure the instance type, disk image, and user data for bare metal instances created from this catalog item.',
          )}
        />
      </StackItem>
      <StackItem>
        <OsacForm>
          <CatalogField name="instanceType">
            {({ valueName, isDisabled, isRequired, labelInfo }) => (
              <BareMetalInstanceTypeField
                name={valueName}
                fieldId="bare-metal-catalog-instance-type"
                isDisabled={isDisabled}
                isRequired={isRequired}
                labelInfo={labelInfo}
                autoSelectSingleOption={false}
              />
            )}
          </CatalogField>
          <CatalogField name="diskImage">
            {({ valueName, isDisabled, isRequired, labelInfo }) => (
              <DiskImageField
                name={valueName}
                guestOsFamilyName="guestOsFamily"
                fieldId="bare-metal-catalog-disk-image"
                isDisabled={isDisabled}
                isRequired={isRequired}
                labelInfo={labelInfo}
                autoSelectSingleOption={false}
              />
            )}
          </CatalogField>
          <CatalogField name="userData">
            {({ valueName, isDisabled, isRequired, labelInfo }) => (
              <CatalogUserDataInput
                valueName={valueName}
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

export default BareMetalCatalogConfigurationStep;
