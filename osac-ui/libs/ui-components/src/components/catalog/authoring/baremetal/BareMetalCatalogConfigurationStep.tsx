import { Content, Stack, StackItem, Title } from '@patternfly/react-core';

import { useTranslation } from '../../../../hooks/useTranslation';
import BareMetalInstanceTypeField from '../../../BareMetalInstance/CreateWizard/fields/BareMetalInstanceTypeField';
import { CATALOG_PROVISION_MULTILINE_TEXTAREA } from '../../../Form/constants';
import { InputField } from '../../../Form/InputField';
import OsacForm from '../../../Form/OsacForm';
import DiskImageField from '../../../ProvisioningWizard/fields/DiskImageField';
import CatalogField from '../CatalogField';

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
        <Content component="p">
          {t(
            'Optionally set instance type, disk image, and user data defaults for this catalog item. Leave fields Not set if tenants should choose when creating a bare metal instance.',
          )}
        </Content>
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
              />
            )}
          </CatalogField>
          <CatalogField name="userData">
            {({ valueName, isDisabled, isRequired, labelInfo }) => (
              // Workaround: project-scoped catalog items are not supported in
              // the UI yet, so catalog item user data is limited to text.
              <InputField
                name={valueName}
                label={t('User data')}
                fieldId="bare-metal-catalog-user-data"
                multiline
                rows={CATALOG_PROVISION_MULTILINE_TEXTAREA.rows}
                resizeOrientation={CATALOG_PROVISION_MULTILINE_TEXTAREA.resizeOrientation}
                helperText={t('Optional cloud-init user data (max 64 KB).')}
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
