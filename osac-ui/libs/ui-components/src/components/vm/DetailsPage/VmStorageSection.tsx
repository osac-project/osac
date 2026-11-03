import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';

import type { ComputeInstance } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import CatalogStorageTable, {
  type CatalogStorageTableRow,
} from '../../catalog/details/CatalogStorageTable';
import { ResourceConditionsTable } from '../../Resource/ResourceConditionsTable';

interface VmStorageSectionProps {
  vm: ComputeInstance;
}

const VmStorageSection = ({ vm }: VmStorageSectionProps) => {
  const { t } = useTranslation();
  const bootDisk = vm.spec?.bootDisk;
  const additionalDisks = vm.spec?.additionalDisks?.items ?? [];
  const additionalDiskRows: CatalogStorageTableRow[] = additionalDisks.map((disk, index) => ({
    id: `additional-disk-${index}`,
    size: disk.sizeGib === undefined ? t('Not set') : `${disk.sizeGib} GB`,
    storageTier: disk.storageTier?.name || disk.storageTier?.id || t('Not set'),
  }));

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Storage')}
        </Title>
      </StackItem>
      <StackItem>
        <DescriptionList isCompact>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Boot disk size')}</DescriptionListTerm>
            <DescriptionListDescription>
              {bootDisk?.sizeGib === undefined ? t('Not set') : `${bootDisk.sizeGib} GB`}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Boot storage tier')}</DescriptionListTerm>
            <DescriptionListDescription>
              {bootDisk?.storageTier?.name || bootDisk?.storageTier?.id || t('Not set')}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Additional disks')}</DescriptionListTerm>
            <DescriptionListDescription>
              <CatalogStorageTable rows={additionalDiskRows} />
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </StackItem>
      <StackItem>
        <Title headingLevel="h3" size="md">
          {t('Conditions')}
        </Title>
      </StackItem>
      <StackItem>
        <ResourceConditionsTable
          ariaLabel={t('Virtual machine conditions')}
          conditions={vm.status?.conditions ?? []}
          conditionResourceKind="compute_instance"
        />
      </StackItem>
    </Stack>
  );
};

export default VmStorageSection;
