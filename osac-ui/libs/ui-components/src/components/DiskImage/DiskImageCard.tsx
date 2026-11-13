import {
  CardBody,
  CardFooter,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Divider,
} from '@patternfly/react-core';
import { RhUiStorageFillIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-storage-fill-icon';

import { type DiskImage, GuestOSFamily } from '@osac/types';
import type { Tenant } from '@osac/types/osac/private/v1/tenant_type_pb';
import DiskImageActionsMenu from '@osac/ui-components/components/DiskImage/DiskImageActionsMenu';
import DiskImageLifecycleLabel from '@osac/ui-components/components/DiskImage/DiskImageLifecycleLabel';
import ListPageCard from '@osac/ui-components/components/Page/ListPageCard';
import OsacCardFooterRow from '@osac/ui-components/components/Primitives/OsacCardFooterRow';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField';

import { DISK_IMAGE_NAME_PREVIEW_LENGTH, architectureLabels, guestOsFamilyLabels } from './utils';
import { useTranslation } from '../../hooks/useTranslation';
import { Timestamp } from '../Primitives/Timestamp';

export interface DiskImageCardProps {
  diskImage: DiskImage;
  tenants: Tenant[];
}

const DiskImageCard = ({ diskImage, tenants }: DiskImageCardProps) => {
  const { t } = useTranslation();
  const guestOsFamilyText = guestOsFamilyLabels(t);
  const architectureText = architectureLabels(t);

  const architecture = diskImage.spec?.architecture ?? [];
  const tenantId = diskImage.metadata?.tenant;
  const isGlobal = tenantId === 'shared';
  const tenantName = tenants.find((tenant) => tenant.id === tenantId)?.metadata?.name;

  return (
    <ListPageCard
      title={
        <ResourceNameField
          resource={diskImage}
          detailsUrl={`/admin/infrastructure/disk-images/${diskImage.id}`}
          maxCharsDisplayed={DISK_IMAGE_NAME_PREVIEW_LENGTH}
        />
      }
      icon={<RhUiStorageFillIcon />}
      statusLabel={<DiskImageLifecycleLabel lifecycle={diskImage.spec?.lifecycle} />}
      actionMenu={<DiskImageActionsMenu diskImage={diskImage} />}
    >
      <CardBody>
        <DescriptionList isCompact isHorizontal>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Guest OS family')}</DescriptionListTerm>
            <DescriptionListDescription>
              {
                guestOsFamilyText[
                  diskImage.spec?.guestOsFamily ?? GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED
                ]
              }
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Architecture')}</DescriptionListTerm>
            <DescriptionListDescription>
              {architecture.length
                ? architecture
                    .map((value) => architectureText[value] ?? t('Unspecified'))
                    .join(', ')
                : '—'}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Scope')}</DescriptionListTerm>
            <DescriptionListDescription>
              {isGlobal ? t('Global') : (tenantName ?? tenantId)}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
      <Divider />
      <CardFooter>
        <OsacCardFooterRow
          term={t('Created')}
          description={<Timestamp value={diskImage.metadata?.creationTimestamp} />}
        />
      </CardFooter>
    </ListPageCard>
  );
};

export default DiskImageCard;
