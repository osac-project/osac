import { useMemo } from 'react';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import { ServiceAccount } from '@osac/types';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import ServiceAccountStatusLabel from './ServiceAccountStatusLabel';
import { Timestamp } from '../Primitives/Timestamp';
import ResourceNameField from '../Resource/ResourceNameField';

interface ServiceAccountsTableProps {
  serviceAccounts: ServiceAccount[];
}

const ServiceAccountsTable = ({ serviceAccounts }: ServiceAccountsTableProps) => {
  const { t } = useTranslation();

  const columnLabels = useMemo(
    () => ({
      name: t('Name'),
      status: t('Status'),
      created: t('Created'),
    }),
    [t],
  );

  return (
    <Table aria-label={t('Service accounts')} variant="compact">
      <Thead>
        <Tr>
          <Th>{columnLabels.name}</Th>
          <Th>{columnLabels.status}</Th>
          <Th>{columnLabels.created}</Th>
        </Tr>
      </Thead>
      <Tbody>
        {serviceAccounts.map((serviceAccount) => (
          <Tr key={serviceAccount.id}>
            <Td dataLabel={columnLabels.name}>
              <ResourceNameField resource={serviceAccount} />
            </Td>
            <Td dataLabel={columnLabels.status}>
              <ServiceAccountStatusLabel serviceAccount={serviceAccount} />
            </Td>
            <Td dataLabel={columnLabels.created}>
              <Timestamp value={serviceAccount.metadata?.creationTimestamp} />
            </Td>
          </Tr>
        ))}
      </Tbody>
    </Table>
  );
};

export default ServiceAccountsTable;
