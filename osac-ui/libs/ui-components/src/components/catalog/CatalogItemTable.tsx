import { FC } from 'react';
import { Label } from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { ComputeInstanceCatalogItem } from '@osac/types';
import CatalogItemLaunchButton from '@osac/ui-components/components/catalog/CatalogItemLaunchButton';
import type { CatalogItemResourceLookups } from '@osac/ui-components/components/catalog/catalogItemResourceLookups';
import CatalogItemResources from '@osac/ui-components/components/catalog/CatalogItemResources';
import CatalogItemTenant from '@osac/ui-components/components/catalog/CatalogItemTenant';
import { Timestamp } from '@osac/ui-components/components/Primitives/Timestamp';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField';
import { useSession } from '@osac/ui-components/hooks/use-session';

import CatalogItemActionsMenu from './CatalogItemActionsMenu';
import { type CatalogItem, catalogItemDetailsPath } from './catalogItemDisplay';
import {
  computeCatalogItemFields,
  computeDiskImageReferenceFromPolicy,
  computeInstanceTypeReferenceFromPolicy,
} from './computeCatalogItemResourceDisplay';
import { useTranslation } from '../../hooks/useTranslation';

interface CatalogItemTableProps {
  items: CatalogItem[];
  resourceLookups: CatalogItemResourceLookups;
}

const isComputeCatalogItem = (item: CatalogItem): item is ComputeInstanceCatalogItem =>
  item.$typeName === 'osac.public.v1.ComputeInstanceCatalogItem';

const displayReferenceName = (name: string | undefined): string => name || '-';

const CatalogItemTable: FC<CatalogItemTableProps> = ({ items, resourceLookups }) => {
  const { t } = useTranslation();
  const { role, tenantId } = useSession();
  const nameLabel = t('Name');
  const statusLabel = t('Status');
  const configurationLabel = t('Configuration');
  const instanceTypeLabel = t('Instance type');
  const diskImageLabel = t('Disk image');
  const tenantLabel = t('Visibility');
  const createdLabel = t('Created');
  const addedLabel = t('Added');
  const actionsLabel = t('Actions');
  const isVmTable = items[0] ? isComputeCatalogItem(items[0]) : false;

  return (
    <Table aria-label={t('Catalog items')} variant="compact">
      <Thead>
        <Tr>
          <Th>{nameLabel}</Th>
          <Th>{statusLabel}</Th>
          {isVmTable ? (
            <>
              <Th>{instanceTypeLabel}</Th>
              <Th>{diskImageLabel}</Th>
            </>
          ) : (
            <Th>{configurationLabel}</Th>
          )}
          {role === 'admin' ? (
            <>
              <Th>{tenantLabel}</Th>
              <Th>{createdLabel}</Th>
            </>
          ) : role === 'tenant-admin' ? (
            <>
              <Th>{addedLabel}</Th>
            </>
          ) : null}
          <Th aria-label={actionsLabel} />
        </Tr>
      </Thead>
      <Tbody>
        {items.map((item) => (
          <Tr key={item.id}>
            <Td dataLabel={nameLabel}>
              <ResourceNameField resource={item} detailsUrl={catalogItemDetailsPath(item)} />
            </Td>
            <Td dataLabel={statusLabel}>
              {item.published ? (
                <Label color="green" isCompact>
                  {t('Live')}
                </Label>
              ) : (
                <Label isCompact>{t('Unpublished')}</Label>
              )}
            </Td>
            {isComputeCatalogItem(item) ? (
              <>
                <Td dataLabel={instanceTypeLabel}>
                  {displayReferenceName(
                    computeInstanceTypeReferenceFromPolicy(
                      computeCatalogItemFields(item)?.instanceType,
                    )?.name,
                  )}
                </Td>
                <Td dataLabel={diskImageLabel}>
                  {displayReferenceName(
                    computeDiskImageReferenceFromPolicy(computeCatalogItemFields(item)?.diskImage)
                      ?.name,
                  )}
                </Td>
              </>
            ) : (
              <Td dataLabel={configurationLabel}>
                <CatalogItemResources catalogItem={item} resourceLookups={resourceLookups} />
              </Td>
            )}
            {role === 'admin' ? (
              <>
                <Td dataLabel={tenantLabel}>
                  <CatalogItemTenant catalogItem={item} />
                </Td>
                <Td dataLabel={createdLabel}>
                  <Timestamp value={item.metadata?.creationTimestamp} format="Time" />
                </Td>
              </>
            ) : role === 'tenant-admin' ? (
              <>
                <Td dataLabel={addedLabel}>
                  <Timestamp value={item.metadata?.creationTimestamp} format="Date" />
                </Td>
              </>
            ) : null}
            <Td dataLabel={actionsLabel} isActionCell>
              {role === 'tenant-user' ? (
                <CatalogItemLaunchButton isDisabled={!item.published} catalogItem={item} />
              ) : (
                <CatalogItemActionsMenu item={item} role={role} tenantId={tenantId} />
              )}
            </Td>
          </Tr>
        ))}
      </Tbody>
    </Table>
  );
};

export default CatalogItemTable;
