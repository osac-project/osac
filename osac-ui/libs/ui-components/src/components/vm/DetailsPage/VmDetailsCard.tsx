import { useState } from 'react';
import type { ReactNode } from 'react';
import {
  Button,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  Grid,
  GridItem,
  Stack,
  StackItem,
  Title,
  Tooltip,
} from '@patternfly/react-core';
import PencilAltIcon from '@patternfly/react-icons/dist/esm/icons/pencil-alt-icon';
import type { TFunction } from 'i18next';

import {
  type ComputeInstance,
  ComputeInstanceState,
  type DiskImage,
  GuestOSFamily,
  type InstanceType,
} from '@osac/types';

import EditInstanceTypeModal from './EditInstanceTypeModal';
import VmExternalIpSection from './VmExternalIpSection';
import { useTranslation } from '../../../hooks/useTranslation';
import { VmStatusLabel } from '../../../VmStatusLabel';
import { guestOsFamilyLabels } from '../../DiskImage/DiskImageTable';
import { Timestamp } from '../../Primitives/Timestamp';
import ResourceLink from '../../Resource/ResourceLink';
import UserDataDetailValue from '../../UserData/UserDataDetailValue';
import { formatInstanceTypeDisplayName } from '../utils';

interface VmDetailsCardProps {
  vm: ComputeInstance;
  instanceType?: InstanceType;
  isInstanceTypeLoading?: boolean;
  diskImage?: DiskImage;
  isDiskImageLoading?: boolean;
}

interface DetailRowProps {
  label: string;
  children: ReactNode;
}

const EMPTY_VALUE = '-';

const DetailRow = ({ label, children }: DetailRowProps) => (
  <DescriptionListGroup>
    <DescriptionListTerm>{label}</DescriptionListTerm>
    <DescriptionListDescription>{children}</DescriptionListDescription>
  </DescriptionListGroup>
);

const canEditInstanceType = (vm: ComputeInstance): boolean => {
  const state = vm.status?.state;
  return (
    state === ComputeInstanceState.RUNNING ||
    state === ComputeInstanceState.PAUSED ||
    state === ComputeInstanceState.STOPPED
  );
};

const disabledInstanceTypeTooltip = (vm: ComputeInstance, t: TFunction): string =>
  vm.status?.state === ComputeInstanceState.DELETING
    ? t('This virtual machine is being deleted.')
    : t(
        'The instance type can be changed when the virtual machine is running, paused, or stopped.',
      );

const displayVmValue = (value?: string): string => value?.trim() || EMPTY_VALUE;

const VmDetailsCard = ({
  vm,
  instanceType,
  isInstanceTypeLoading = false,
  diskImage,
  isDiskImageLoading = false,
}: VmDetailsCardProps) => {
  const { t } = useTranslation();
  const [editOpen, setEditOpen] = useState(false);
  const catalogItem = vm.spec?.catalogItem;
  const diskImageReference = vm.spec?.diskImage;
  const sshKey = vm.spec?.sshKey;
  const sshKeyLabel = sshKey?.name || sshKey?.id;
  const guestOsFamily = diskImage?.spec?.guestOsFamily;
  const guestOsFamilyLabel = guestOsFamily
    ? (guestOsFamilyLabels(t)[guestOsFamily] ?? EMPTY_VALUE)
    : EMPTY_VALUE;
  const diskImageLabel =
    diskImage?.metadata?.name || diskImageReference?.name || diskImageReference?.id;
  const instanceTypeLabel = isInstanceTypeLoading
    ? t('Loading')
    : formatInstanceTypeDisplayName(instanceType, undefined, vm.spec?.instanceType?.id);
  const vcpus = instanceType?.spec?.vcpus;
  const memoryGib = instanceType?.spec?.memoryGib;
  const gpu = instanceType?.spec?.gpu;
  const hasWindowsGuest = guestOsFamily === GuestOSFamily.GUEST_OS_FAMILY_WINDOWS;
  const userDataAccessLabel = hasWindowsGuest ? t('Sysprep (Unattend.xml)') : t('Cloud-init');
  const canEdit = canEditInstanceType(vm);
  const editButton = (
    <Button
      variant="plain"
      icon={<PencilAltIcon />}
      aria-label={t('Edit instance type')}
      isDisabled={!canEdit}
      onClick={() => setEditOpen(true)}
    />
  );

  return (
    <>
      <Grid hasGutter>
        <GridItem md={4}>
          <Stack hasGutter>
            <StackItem>
              <Title headingLevel="h2" size="lg">
                {t('Overview')}
              </Title>
            </StackItem>
            <StackItem>
              <DescriptionList isCompact>
                <DetailRow label={t('Project')}>{displayVmValue(vm.metadata?.project)}</DetailRow>
                <DetailRow label={t('Status')}>
                  <VmStatusLabel state={vm.status?.state} />
                </DetailRow>
                <DetailRow label={t('Created')}>
                  <Timestamp value={vm.metadata?.creationTimestamp} fallback={EMPTY_VALUE} />
                </DetailRow>
                <DetailRow label={t('Catalog item')}>
                  {displayVmValue(catalogItem?.name || catalogItem?.id)}
                </DetailRow>
              </DescriptionList>
            </StackItem>
          </Stack>
        </GridItem>

        <GridItem md={4}>
          <Stack hasGutter>
            <StackItem>
              <Title headingLevel="h2" size="lg">
                {t('Configuration')}
              </Title>
            </StackItem>
            <StackItem>
              <DescriptionList isCompact>
                <DetailRow label={t('Guest operating system family')}>
                  {isDiskImageLoading ? t('Loading') : guestOsFamilyLabel}
                </DetailRow>
                <DetailRow label={t('Disk image')}>{displayVmValue(diskImageLabel)}</DetailRow>
                <DetailRow label={t('Instance type')}>
                  <Flex
                    spaceItems={{ default: 'spaceItemsSm' }}
                    alignItems={{ default: 'alignItemsCenter' }}
                  >
                    {instanceTypeLabel}
                    {canEdit ? (
                      editButton
                    ) : (
                      <Tooltip content={disabledInstanceTypeTooltip(vm, t)}>
                        <span tabIndex={0}>{editButton}</span>
                      </Tooltip>
                    )}
                  </Flex>
                </DetailRow>
                <DetailRow label={t('vCPUs')}>
                  {vcpus === undefined ? EMPTY_VALUE : vcpus}
                </DetailRow>
                <DetailRow label={t('Memory')}>
                  {memoryGib === undefined ? EMPTY_VALUE : `${memoryGib} GiB`}
                </DetailRow>
                {gpu ? (
                  <>
                    <DetailRow label={t('GPU count')}>{gpu.count}</DetailRow>
                    <DetailRow label={t('GPU resource name')}>
                      {displayVmValue(gpu.resourceName)}
                    </DetailRow>
                  </>
                ) : null}
              </DescriptionList>
            </StackItem>
          </Stack>
        </GridItem>

        <GridItem md={4}>
          <Stack hasGutter>
            <StackItem>
              <Title headingLevel="h2" size="lg">
                {t('Access & Initial run')}
              </Title>
            </StackItem>
            <StackItem>
              <DescriptionList isCompact>
                <VmExternalIpSection vm={vm} />
                {!hasWindowsGuest && (
                  <DetailRow label={t('SSH public key')}>
                    {sshKey?.id && sshKeyLabel ? (
                      <ResourceLink basePath="/secrets" id={sshKey.id} name={sshKeyLabel} />
                    ) : (
                      displayVmValue(sshKeyLabel)
                    )}
                  </DetailRow>
                )}
                <DetailRow label={userDataAccessLabel}>
                  <UserDataDetailValue
                    userData={vm.spec?.userDataSource}
                    emptyValue={t('Not configured')}
                  />
                </DetailRow>
              </DescriptionList>
            </StackItem>
          </Stack>
        </GridItem>
      </Grid>

      {editOpen ? <EditInstanceTypeModal vm={vm} onClose={() => setEditOpen(false)} /> : null}
    </>
  );
};

export default VmDetailsCard;
