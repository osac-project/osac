import { Content, Stack, StackItem, Title } from '@patternfly/react-core';

import { useTranslation } from '../../../../hooks/useTranslation';
import OsacForm from '../../../Form/OsacForm';
import ProvisioningFieldGroup from '../../../ProvisioningWizard/fields/ProvisioningFieldGroup';
import { AdditionalDisksArrayField } from '../../../vm/CreateWizard/AdditionalDisksArrayField';
import BootDiskSizeField from '../../../vm/CreateWizard/fields/BootDiskSizeField';
import BootStorageTierField from '../../../vm/CreateWizard/fields/BootStorageTierField';
import CatalogField from '../CatalogField';

const ComputeCatalogStorageStep = () => {
  const { t } = useTranslation();

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Storage')}
        </Title>
      </StackItem>
      <StackItem>
        <Content component="p">
          {t(
            'Optionally set boot disk size, storage tier, and additional disk defaults for this catalog item. Leave fields Not set if tenants should choose when creating a virtual machine.',
          )}
        </Content>
      </StackItem>
      <StackItem>
        <OsacForm>
          <CatalogField name="bootDiskSizeGib">
            {({ valueName, isDisabled, labelInfo }) => (
              <BootDiskSizeField
                name={valueName}
                fieldId="compute-catalog-boot-disk-size"
                isDisabled={isDisabled}
                isRequired={false}
                labelInfo={labelInfo}
              />
            )}
          </CatalogField>
          <CatalogField name="bootDiskStorageTier">
            {({ valueName, isDisabled, labelInfo }) => (
              <BootStorageTierField
                name={valueName}
                isDisabled={isDisabled}
                labelInfo={labelInfo}
              />
            )}
          </CatalogField>
          <CatalogField name="additionalDisks">
            {({ valueName, isDisabled, labelInfo }) => (
              <ProvisioningFieldGroup
                title={t('Additional disks')}
                titleId="compute-catalog-additional-disks"
                actions={labelInfo}
              >
                <AdditionalDisksArrayField name={valueName} isDisabled={isDisabled} />
              </ProvisioningFieldGroup>
            )}
          </CatalogField>
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default ComputeCatalogStorageStep;
