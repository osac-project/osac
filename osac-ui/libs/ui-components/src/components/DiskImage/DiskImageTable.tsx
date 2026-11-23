import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import { type DiskImage, GuestOSFamily } from '@osac/types';
import { type Tenant } from '@osac/types/private';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';

import DiskImageActionsMenu from './DiskImageActionsMenu';
import DiskImageLifecycleLabel from './DiskImageLifecycleLabel';
import { DISK_IMAGE_NAME_PREVIEW_LENGTH, architectureLabels, guestOsFamilyLabels } from './utils';
import { useTranslation } from '../../hooks/useTranslation';
import { Timestamp } from '../Primitives/Timestamp';

const NAME_COLUMN_WIDTH = 20;
const LIFECYCLE_COLUMN_WIDTH = 15;
const GUEST_OS_COLUMN_WIDTH = 15;
const ARCHITECTURE_COLUMN_WIDTH = 20;
const SCOPE_COLUMN_WIDTH = 15;
const CREATED_COLUMN_WIDTH = 15;

interface DiskImageTableProps {
  diskImages: DiskImage[];
  tenants: Tenant[];
}

const DiskImageTable = ({ diskImages, tenants }: DiskImageTableProps) => {
  const { t } = useTranslation();
  const guestOsFamilyText = guestOsFamilyLabels(t);
  const architectureText = architectureLabels(t);

  return (
    <Table aria-label={t('Disk images')} variant="compact">
      <Thead>
        <Tr>
          <Th width={NAME_COLUMN_WIDTH}>{t('Name')}</Th>
          <Th width={LIFECYCLE_COLUMN_WIDTH}>{t('Lifecycle')}</Th>
          <Th width={GUEST_OS_COLUMN_WIDTH}>{t('Guest OS family')}</Th>
          <Th width={ARCHITECTURE_COLUMN_WIDTH}>{t('Architecture')}</Th>
          <Th width={SCOPE_COLUMN_WIDTH}>{t('Scope')}</Th>
          <Th width={CREATED_COLUMN_WIDTH}>{t('Created')}</Th>
          <Th aria-label={t('Actions')} />
        </Tr>
      </Thead>
      <Tbody>
        {diskImages.map((diskImage) => {
          const architecture = diskImage.spec?.architecture ?? [];
          const tenantId = diskImage.metadata?.tenant;
          const isGlobal = tenantId === 'shared';
          const tenantName = tenants.find((tenant) => tenant.id === tenantId)?.metadata?.name;

          return (
            <Tr key={diskImage.id}>
              <Td dataLabel={t('Name')} modifier="truncate" width={NAME_COLUMN_WIDTH}>
                <ResourceNameField
                  resource={diskImage}
                  detailsUrl={`/admin/infrastructure/disk-images/${diskImage.id}`}
                  maxCharsDisplayed={DISK_IMAGE_NAME_PREVIEW_LENGTH}
                />
              </Td>
              <Td dataLabel={t('Lifecycle')} width={LIFECYCLE_COLUMN_WIDTH}>
                <DiskImageLifecycleLabel lifecycle={diskImage.spec?.lifecycle} />
              </Td>
              <Td dataLabel={t('Guest OS family')} width={GUEST_OS_COLUMN_WIDTH}>
                {
                  guestOsFamilyText[
                    diskImage.spec?.guestOsFamily ?? GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED
                  ]
                }
              </Td>
              <Td dataLabel={t('Architecture')} width={ARCHITECTURE_COLUMN_WIDTH}>
                {architecture.length
                  ? architecture
                      .map((value) => architectureText[value] ?? t('Unspecified'))
                      .join(', ')
                  : '—'}
              </Td>
              <Td dataLabel={t('Scope')} width={SCOPE_COLUMN_WIDTH}>
                {isGlobal ? t('Global') : (tenantName ?? tenantId)}
              </Td>
              <Td dataLabel={t('Created')} width={CREATED_COLUMN_WIDTH}>
                <Timestamp value={diskImage.metadata?.creationTimestamp} />
              </Td>
              <Td dataLabel={t('Actions')} isActionCell>
                <DiskImageActionsMenu diskImage={diskImage} />
              </Td>
            </Tr>
          );
        })}
      </Tbody>
    </Table>
  );
};

export default DiskImageTable;
