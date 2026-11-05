import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
} from '@patternfly/react-core';

import { type ComputeInstance, ComputeInstanceState } from '@osac/types';
import InstanceCard from '@osac/ui-components/components/InstanceCard/InstanceCard';
import { Timestamp } from '@osac/ui-components/components/Primitives/Timestamp';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import VmActionMenu from './VmActionsMenu';
import { VmStatusLabel } from '../../../VmStatusLabel';

interface VmInstanceCardProps {
  vm: ComputeInstance;
  /** Shows the tenant, for cloud provider admins who see every tenant's workloads. */
  showTenant?: boolean;
}

const VmInstanceCard = ({ vm, showTenant = false }: VmInstanceCardProps) => {
  const { t } = useTranslation();
  const state = vm.status?.state;
  const locked = state === ComputeInstanceState.DELETING;
  const detailsUrl = locked ? undefined : `/vms/${encodeURIComponent(vm.id)}`;
  const externalIp = vm.status?.externalIpAddress;

  return (
    <InstanceCard
      kind="vm"
      statusLabel={<VmStatusLabel state={state} />}
      actionMenu={locked ? null : <VmActionMenu vm={vm} />}
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
              {locked ? '—' : externalIp || '—'}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      }
      footer={
        <Flex direction={{ default: 'column' }} gap={{ default: 'gapSm' }}>
          {showTenant ? (
            <FlexItem>
              <Flex
                flexWrap={{ default: 'nowrap' }}
                gap={{ default: 'gapLg' }}
                justifyContent={{ default: 'justifyContentSpaceBetween' }}
              >
                <FlexItem>{t('Tenant')}</FlexItem>
                <FlexItem>{vm.metadata?.tenant || '—'}</FlexItem>
              </Flex>
            </FlexItem>
          ) : null}
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
              <FlexItem>
                <Timestamp value={vm.metadata?.creationTimestamp} />
              </FlexItem>
            </Flex>
          </FlexItem>
        </Flex>
      }
    />
  );
};

export default VmInstanceCard;
