import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Divider,
  Grid,
  GridItem,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';

import type { ComputeInstance } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { ResourceConditionsTable } from '../../Resource/ResourceConditionsTable';
import AdditionalDisksTable from '../../shared/AdditionalDisksTable';

import './VmStorageSection.css';

interface VmStorageSectionProps {
  vm: ComputeInstance;
}

const EMPTY_VALUE = '-';

const VmStorageSection = ({ vm }: VmStorageSectionProps) => {
  const { t } = useTranslation();
  const bootDisk = vm.spec?.bootDisk;
  const additionalDisks = vm.spec?.additionalDisks?.items ?? [];

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
              {bootDisk?.sizeGib === undefined ? EMPTY_VALUE : `${bootDisk.sizeGib} GiB`}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Boot storage tier')}</DescriptionListTerm>
            <DescriptionListDescription>
              {bootDisk?.storageTier?.name || bootDisk?.storageTier?.id || EMPTY_VALUE}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Additional disks')}</DescriptionListTerm>
            <DescriptionListDescription>
              <Grid>
                <GridItem lg={6}>
                  {additionalDisks.length > 0 ? (
                    <AdditionalDisksTable
                      disks={additionalDisks}
                      className="osac-vm-storage-table"
                    />
                  ) : (
                    EMPTY_VALUE
                  )}
                </GridItem>
                <GridItem>
                  <Divider />
                </GridItem>
              </Grid>
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
