import { Card, CardBody, CardTitle, Content } from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { Cluster } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { displayValue } from '../../../utils/detailFormatters';

interface ClusterNodeSetsTabProps {
  cluster: Cluster;
}

const ClusterNodeSetsTab = ({ cluster }: ClusterNodeSetsTabProps) => {
  const { t } = useTranslation();

  const nodeSetEntries = Object.entries(cluster.spec?.nodeSets ?? {});

  return (
    <Card isFullHeight>
      <CardTitle>{t('Node sets')}</CardTitle>
      <CardBody>
        {nodeSetEntries.length > 0 ? (
          <Table aria-label={t('Cluster node sets')} variant="compact">
            <Thead>
              <Tr>
                <Th>{t('Name')}</Th>
                <Th>{t('Host type')}</Th>
                <Th>{t('Size')}</Th>
              </Tr>
            </Thead>
            <Tbody>
              {nodeSetEntries.map(([key, nodeSet]) => (
                <Tr key={key}>
                  <Td dataLabel={t('Name')}>{key}</Td>
                  <Td dataLabel={t('Host type')}>
                    {displayValue(nodeSet.baremetalInstanceType?.name)}
                  </Td>
                  <Td dataLabel={t('Size')}>{nodeSet.size}</Td>
                </Tr>
              ))}
            </Tbody>
          </Table>
        ) : (
          <Content component="p">{t('No node sets configured.')}</Content>
        )}
      </CardBody>
    </Card>
  );
};

export default ClusterNodeSetsTab;
