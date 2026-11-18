import { Alert } from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { SecurityGroup } from '@osac/types';

import { SecurityGroupStatusLabel } from './SecurityGroupStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import ResourceNameField from '../Resource/ResourceNameField';
import { SubtleContent } from '../SubtleContent/SubtleContent';

interface SecurityGroupsTableProps {
  securityGroups: readonly SecurityGroup[];
  isLoading?: boolean;
  error?: unknown;
}

/** The SecurityGroups of one VirtualNetwork, shared by the details tab and later pages. */
const SecurityGroupsTable = ({ securityGroups, isLoading, error }: SecurityGroupsTableProps) => {
  const { t } = useTranslation();

  if (isLoading) {
    return <SubtleContent component="p">{t('Loading security groups...')}</SubtleContent>;
  }
  if (error) {
    return (
      <Alert variant="danger" title={t('Failed to load security groups')} isInline>
        {getErrorMessage(error)}
      </Alert>
    );
  }
  if (securityGroups.length === 0) {
    return (
      <SubtleContent component="p">
        {t('No security groups yet. Create one to get started.')}
      </SubtleContent>
    );
  }

  return (
    <Table aria-label={t('Security groups')} variant="compact" borders>
      <Thead>
        <Tr>
          <Th>{t('Name')}</Th>
          <Th>{t('Status')}</Th>
          <Th>{t('Inbound Rules')}</Th>
          <Th>{t('Outbound Rules')}</Th>
        </Tr>
      </Thead>
      <Tbody>
        {securityGroups.map((securityGroup) => (
          <Tr key={securityGroup.id}>
            <Td dataLabel={t('Name')}>
              <ResourceNameField
                resource={securityGroup}
                detailsUrl={`/networking/security-groups/${securityGroup.id}`}
              />
            </Td>
            <Td dataLabel={t('Status')}>
              <SecurityGroupStatusLabel
                state={securityGroup.status?.state}
                message={securityGroup.status?.message}
              />
            </Td>
            <Td dataLabel={t('Inbound Rules')}>{securityGroup.spec?.ingress?.length ?? 0}</Td>
            <Td dataLabel={t('Outbound Rules')}>{securityGroup.spec?.egress?.length ?? 0}</Td>
          </Tr>
        ))}
      </Tbody>
    </Table>
  );
};

export default SecurityGroupsTable;
