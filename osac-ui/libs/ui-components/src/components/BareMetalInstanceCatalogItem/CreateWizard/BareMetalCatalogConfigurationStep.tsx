import { Stack, StackItem, Title } from '@patternfly/react-core';

import { BareMetalInstanceTypes } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import CatalogField from '../../CatalogItem/CatalogField';
import CatalogFieldPolicyDescription from '../../CatalogItem/CatalogFieldPolicyDescription';
import CatalogUserDataInput from '../../CatalogItem/CatalogUserDataInput';
import OsacForm from '../../Form/OsacForm';
import { ResourceSelectField } from '../../Form/ResourceSelectField';
import DiskImageField from '../../ProvisioningWizard/fields/DiskImageField';

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
              <ResourceSelectField
                name={valueName}
                label={t('Instance type')}
                fieldId="bare-metal-catalog-instance-type"
                service={BareMetalInstanceTypes}
                isDisabled={isDisabled}
                isRequired={isRequired}
                labelInfo={labelInfo}
                autoSelectSingleOption={false}
                placeholder={t('Select an instance type')}
                loadErrorTitle={t('Could not load instance types')}
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
