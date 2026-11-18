import { Alert } from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { Subnet } from '@osac/types';

import { SubnetStatusLabel } from './SubnetStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import ResourceNameField from '../Resource/ResourceNameField';
import { SubtleContent } from '../SubtleContent/SubtleContent';

interface SubnetsTableProps {
  subnets: readonly Subnet[];
  isLoading?: boolean;
  error?: unknown;
}

/** The Subnets of one VirtualNetwork, shared by the details tab and later pages. */
const SubnetsTable = ({ subnets, isLoading, error }: SubnetsTableProps) => {
  const { t } = useTranslation();

  if (isLoading) {
    return <SubtleContent component="p">{t('Loading subnets...')}</SubtleContent>;
  }
  if (error) {
    return (
      <Alert variant="danger" title={t('Failed to load subnets')} isInline>
        {getErrorMessage(error)}
      </Alert>
    );
  }
  if (subnets.length === 0) {
    return (
      <SubtleContent component="p">{t('No subnets yet. Create one to get started.')}</SubtleContent>
    );
  }

  return (
    <Table aria-label={t('Subnets')} variant="compact" borders>
      <Thead>
        <Tr>
          <Th>{t('Name')}</Th>
          <Th>{t('Status')}</Th>
          <Th>{t('CIDR')}</Th>
        </Tr>
      </Thead>
      <Tbody>
        {subnets.map((subnet) => (
          <Tr key={subnet.id}>
            <Td dataLabel={t('Name')}>
              <ResourceNameField
                resource={subnet}
                detailsUrl={`/networking/subnets/${subnet.id}`}
              />
            </Td>
            <Td dataLabel={t('Status')}>
              <SubnetStatusLabel state={subnet.status?.state} message={subnet.status?.message} />
            </Td>
            <Td dataLabel={t('CIDR')}>{subnet.spec?.ipv4Cidr ?? '—'}</Td>
          </Tr>
        ))}
      </Tbody>
    </Table>
  );
};

export default SubnetsTable;
