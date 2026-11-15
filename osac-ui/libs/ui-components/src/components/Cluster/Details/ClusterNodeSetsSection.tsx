import { Content, Stack, StackItem, Title } from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { Cluster } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { displayValue } from '../../../utils/detailFormatters';

interface ClusterNodeSetsSectionProps {
  cluster: Cluster;
}

const ClusterNodeSetsSection = ({ cluster }: ClusterNodeSetsSectionProps) => {
  const { t } = useTranslation();

  const nodeSetEntries = Object.entries(cluster.spec?.nodeSets ?? {});

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Node sets')}
        </Title>
      </StackItem>
      <StackItem>
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
      </StackItem>
    </Stack>
  );
};

export default ClusterNodeSetsSection;
