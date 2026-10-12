import { Link } from 'react-router-dom';
import {
  Card,
  CardBody,
  CardTitle,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
} from '@patternfly/react-core';

import type { ComputeInstance } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { displayValue } from '../../../utils/detailFormatters';
import { formatBootDiskSizeForReview } from '../../catalogProvision/wizard/catalogOverlay';
import { Timestamp } from '../../Primitives/Timestamp';

interface Props {
  vm: ComputeInstance;
}

const VmDetailsCard = ({ vm }: Props) => {
  const { t } = useTranslation();
  const catalogItem = vm.spec?.catalogItem;
  const instanceType = vm.spec?.instanceType;
  const sshKey = vm.spec?.sshKey;
  const sshKeyLabel = sshKey?.name?.trim() || sshKey?.id?.trim();
  const sshKeyDetailsUrl = sshKey?.id?.trim() ? `/secrets/${sshKey.id.trim()}` : undefined;

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
              {displayValue(instanceType?.name || instanceType?.id)}
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
          {(vm.spec?.additionalDisks ?? []).map((disk, index) => (
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
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Creator')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(vm.metadata?.creator)}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
    </Card>
  );
};

export default VmDetailsCard;
