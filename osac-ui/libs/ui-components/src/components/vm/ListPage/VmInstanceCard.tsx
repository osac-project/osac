import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
} from '@patternfly/react-core';

import { type ComputeInstance, ComputeInstanceState, type InstanceType } from '@osac/types';
import InstanceCard from '@osac/ui-components/components/InstanceCard/InstanceCard';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField';
import VmActionButtons from '@osac/ui-components/components/vm/actions/VmActionButtons';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { VmStatusLabel } from '../../../VmStatusLabel';
import { formatVmCreatedAt } from '../vmInstanceDisplay';

interface VmInstanceCardProps {
  vm: ComputeInstance;
  instanceTypes?: InstanceType[];
  isInstanceTypesLoading?: boolean;
}

const VmInstanceCard = ({ vm }: VmInstanceCardProps) => {
  const { t } = useTranslation();
  const state = vm.status?.state;
  const locked = state === ComputeInstanceState.DELETING;
  const createdAt = formatVmCreatedAt(vm.metadata);
  const detailsUrl = locked ? undefined : `/vms/${encodeURIComponent(vm.id)}`;

  return (
    <InstanceCard
      kind="vm"
      statusLabel={<VmStatusLabel state={state} />}
      actionMenu={locked ? null : <VmActionButtons vm={vm} isMenu />}
      title={<ResourceNameField resource={vm} detailsUrl={detailsUrl} />}
      body={
        <DescriptionList isHorizontal isCompact className="vm-instance-card__specs">
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Instance type')}</DescriptionListTerm>
            <DescriptionListDescription>
              {vm.spec?.instanceType?.name || '-'}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Disk image')}</DescriptionListTerm>
            <DescriptionListDescription>
              {locked ? '—' : vm.spec?.diskImage?.name || '—'}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('External IP')}</DescriptionListTerm>
            <DescriptionListDescription>
              {locked ? '—' : vm.status?.externalIpAddress || '—'}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      }
      footer={
        <Flex direction={{ default: 'column' }} gap={{ default: 'gapSm' }}>
          <FlexItem>
            <Flex
              flexWrap={{ default: 'nowrap' }}
              gap={{ default: 'gapLg' }}
              justifyContent={{ default: 'justifyContentSpaceBetween' }}
            >
              <FlexItem>{t('Project')}</FlexItem>
              <FlexItem>{vm.metadata?.project || t('Default')}</FlexItem>
            </Flex>
          </FlexItem>
          <FlexItem>
            <Flex
              flexWrap={{ default: 'nowrap' }}
              gap={{ default: 'gapLg' }}
              justifyContent={{ default: 'justifyContentSpaceBetween' }}
            >
              <FlexItem>{t('Created')}</FlexItem>
              <FlexItem>{createdAt}</FlexItem>
            </Flex>
          </FlexItem>
        </Flex>
      }
    />
  );
};

export default VmInstanceCard;
