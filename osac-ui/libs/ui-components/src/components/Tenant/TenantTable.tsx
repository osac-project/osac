import { useMemo } from 'react';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { Tenant } from '@osac/types/private';

import TenantActionsMenu from './TenantActionsMenu';
import TenantStatusLabel from './TenantStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';
import { Timestamp } from '../Primitives/Timestamp';
import ResourceNameField from '../Resource/ResourceNameField';

interface TenantTableProps {
  tenants: Tenant[];
}

const TenantTable = ({ tenants }: TenantTableProps) => {
  const { t } = useTranslation();

  const columnLabels = useMemo(
    () => ({
      tenant: t('Tenant'),
      status: t('Status'),
      primaryDomain: t('Domain'),
      registered: t('Registered'),
      actions: t('Actions'),
    }),
    [t],
  );

  return (
    <Table aria-label={t('Tenants')} variant="compact">
      <Thead>
        <Tr>
          <Th>{columnLabels.tenant}</Th>
          <Th>{columnLabels.status}</Th>
          <Th>{columnLabels.primaryDomain}</Th>
          <Th>{columnLabels.registered}</Th>
          <Th aria-label={columnLabels.actions} />
        </Tr>
      </Thead>
      <Tbody>
        {tenants.map((tenant) => (
          <Tr key={tenant.id}>
            <Td dataLabel={columnLabels.tenant}>
              <ResourceNameField resource={tenant} title={tenant.metadata?.name ?? tenant.id} />
            </Td>
            <Td dataLabel={columnLabels.status}>
              <TenantStatusLabel state={tenant.status?.state} />
            </Td>
            <Td dataLabel={columnLabels.primaryDomain}>{tenant.spec?.domains?.[0] ?? '—'}</Td>
            <Td dataLabel={columnLabels.registered}>
              <Timestamp value={tenant.metadata?.creationTimestamp} />
            </Td>
            <Td dataLabel={columnLabels.actions} isActionCell>
              <TenantActionsMenu tenant={tenant} />
            </Td>
          </Tr>
        ))}
      </Tbody>
    </Table>
  );
};

export default TenantTable;
