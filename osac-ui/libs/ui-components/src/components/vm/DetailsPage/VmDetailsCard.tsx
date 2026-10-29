import { useState } from 'react';
import { Link } from 'react-router-dom';
import {
  Button,
  Card,
  CardBody,
  CardTitle,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  Tooltip,
} from '@patternfly/react-core';
import PencilAltIcon from '@patternfly/react-icons/dist/esm/icons/pencil-alt-icon';
import type { TFunction } from 'i18next';

import { type ComputeInstance, ComputeInstanceState } from '@osac/types';

import EditInstanceTypeModal from './EditInstanceTypeModal';
import { useTranslation } from '../../../hooks/useTranslation';
import { displayValue } from '../../../utils/detailFormatters';
import { Timestamp } from '../../Primitives/Timestamp';
import { formatBootDiskSizeForReview } from '../../ProvisioningWizard/reviewFormatters';

interface Props {
  vm: ComputeInstance;
}

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

const VmDetailsCard = ({ vm }: Props) => {
  const { t } = useTranslation();
  const [editOpen, setEditOpen] = useState(false);
  const catalogItem = vm.spec?.catalogItem;
  const instanceType = vm.spec?.instanceType;
  const sshKey = vm.spec?.sshKey;
  const sshKeyLabel = sshKey?.name || sshKey?.id;
  const sshKeyDetailsUrl = sshKey?.id ? `/secrets/${sshKey.id}` : undefined;
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
    <Card isFullHeight>
      <CardTitle>{t('Details')}</CardTitle>
      <CardBody>
        <DescriptionList isCompact>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Catalog item')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(catalogItem?.name || catalogItem?.id)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Name')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(vm.metadata?.name)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('SSH public key')}</DescriptionListTerm>
            <DescriptionListDescription>
              {sshKeyDetailsUrl && sshKeyLabel ? (
                <Link to={sshKeyDetailsUrl}>{sshKeyLabel}</Link>
              ) : (
                displayValue(sshKeyLabel)
              )}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Instance type')}</DescriptionListTerm>
            <DescriptionListDescription>
              <Flex
                spaceItems={{ default: 'spaceItemsSm' }}
                alignItems={{ default: 'alignItemsCenter' }}
              >
                {displayValue(instanceType?.name || instanceType?.id)}
                {canEdit ? (
                  editButton
                ) : (
                  <Tooltip content={disabledInstanceTypeTooltip(vm, t)}>
                    <span tabIndex={0}>{editButton}</span>
                  </Tooltip>
                )}
              </Flex>
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Boot disk')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatBootDiskSizeForReview(vm.spec?.bootDisk?.sizeGib)},{' '}
              {displayValue(
                vm.spec?.bootDisk?.storageTier?.name || vm.spec?.bootDisk?.storageTier?.id,
              )}
            </DescriptionListDescription>
          </DescriptionListGroup>
          {(vm.spec?.additionalDisks?.items ?? []).map((disk, index) => (
            <DescriptionListGroup key={`additional-disk-${index}`}>
              <DescriptionListTerm>
                {t('Additional disk {{number}}', { number: index + 1 })}
              </DescriptionListTerm>
              <DescriptionListDescription>
                {formatBootDiskSizeForReview(disk.sizeGib)},{' '}
                {displayValue(disk.storageTier?.name || disk.storageTier?.id)}
              </DescriptionListDescription>
            </DescriptionListGroup>
          ))}
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Created')}</DescriptionListTerm>
            <DescriptionListDescription>
              <Timestamp value={vm.metadata?.creationTimestamp} />
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
      {editOpen ? <EditInstanceTypeModal vm={vm} onClose={() => setEditOpen(false)} /> : null}
    </Card>
  );
};

export default VmDetailsCard;
