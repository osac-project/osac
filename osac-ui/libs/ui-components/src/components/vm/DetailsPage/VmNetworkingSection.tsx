import { Card, CardBody, CardTitle, Title } from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { ComputeInstance } from '@osac/types';

import { useVmNetworkAttachmentRows } from './useVmNetworkAttachmentRows';
import { useTranslation } from '../../../hooks/useTranslation';
import { SubtleContent } from '../../SubtleContent/SubtleContent';

interface VmNetworkingSectionProps {
  vm: ComputeInstance;
}

const VmNetworkingSection = ({ vm }: VmNetworkingSectionProps) => {
  const { t } = useTranslation();
  const rows = useVmNetworkAttachmentRows(vm);

  return (
    <Card>
      <CardTitle>
        <Title headingLevel="h2" size="lg">
          {t('Networking')}
        </Title>
      </CardTitle>
      <CardBody>
        {rows.length > 0 ? (
          <Table aria-label={t('Networking')} variant="compact" borders>
            <Thead>
              <Tr>
                <Th>{t('Virtual network')}</Th>
                <Th>{t('Subnet')}</Th>
                <Th>{t('Security groups')}</Th>
                <Th>{t('Internal IP')}</Th>
              </Tr>
            </Thead>
            <Tbody>
              {rows.map((row, index) => (
                <Tr key={`network-attachment-${index}`}>
                  <Td dataLabel={t('Virtual network')}>{row.virtualNetwork}</Td>
                  <Td dataLabel={t('Subnet')}>{row.subnet}</Td>
                  <Td dataLabel={t('Security groups')}>{row.securityGroups}</Td>
                  <Td dataLabel={t('Internal IP')}>{row.internalIp}</Td>
                </Tr>
              ))}
            </Tbody>
          </Table>
        ) : (
          <SubtleContent component="p">{t('No virtual networks configured.')}</SubtleContent>
        )}
      </CardBody>
    </Card>
  );
};

export default VmNetworkingSection;
