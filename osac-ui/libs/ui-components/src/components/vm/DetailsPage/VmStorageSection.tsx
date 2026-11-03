import {
  Card,
  CardBody,
  CardTitle,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Title,
} from '@patternfly/react-core';

import type { ComputeInstance } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import CatalogStorageTable, {
  type CatalogStorageTableRow,
} from '../../catalog/details/CatalogStorageTable';

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
    <Card>
      <CardTitle>
        <Title headingLevel="h2" size="lg">
          {t('Storage')}
        </Title>
      </CardTitle>
      <CardBody>
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
      </CardBody>
    </Card>
  );
};

export default VmStorageSection;
