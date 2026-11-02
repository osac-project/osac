import { Divider, Stack, StackItem, Title } from '@patternfly/react-core';

import { useTranslation } from '../../../../hooks/useTranslation';
import OsacForm from '../../../Form/OsacForm';
import ProvisioningFieldGroup from '../../../ProvisioningWizard/fields/ProvisioningFieldGroup';
import { AdditionalDisksArrayField } from '../../../vm/CreateWizard/AdditionalDisksArrayField';
import BootDiskSizeField from '../../../vm/CreateWizard/fields/BootDiskSizeField';
import BootStorageTierField from '../../../vm/CreateWizard/fields/BootStorageTierField';
import CatalogField from '../CatalogField';
import CatalogFieldPolicyDescription from '../CatalogFieldPolicyDescription';

const VMCatalogStorageStep = () => {
  const { t } = useTranslation();

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Storage')}
        </Title>
      </StackItem>
      <StackItem>
        <CatalogFieldPolicyDescription
          description={t(
            'Configure the boot disk size, storage tier, and additional disks for virtual machines created from this catalog item.',
          )}
        />
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
          <Divider />
          <CatalogField name="bootDiskStorageTier">
            {({ valueName, isDisabled, labelInfo }) => (
              <BootStorageTierField
                name={valueName}
                isDisabled={isDisabled}
                labelInfo={labelInfo}
                autoSelectSingleOption={false}
              />
            )}
          </CatalogField>
          <Divider />
          <CatalogField name="additionalDisks">
            {({ valueName, isDisabled, isRequired, labelInfo }) => (
              <ProvisioningFieldGroup
                title={t('Additional disks')}
                titleId="compute-catalog-additional-disks"
                actions={labelInfo}
              >
                <AdditionalDisksArrayField
                  name={valueName}
                  isDisabled={isDisabled}
                  isRequired={isRequired}
                  autoSelectSingleOption={false}
                  emptyMessage={t(
                    'No additional disks. Add disks if this catalog item should include them by default.',
                  )}
                />
              </ProvisioningFieldGroup>
            )}
          </CatalogField>
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default VMCatalogStorageStep;
