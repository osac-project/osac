import { Content, Divider, Stack, StackItem, Title } from '@patternfly/react-core';

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
        <Title headingLevel="h3" size="md">
          {t('Node sets')}
        </Title>
        <Divider />
      </StackItem>
      <StackItem>
        {nodeSetEntries.length > 0 ? (
          <ul
            aria-label={t('Cluster node sets')}
            className="cluster-details__node-sets"
            role="list"
          >
            {nodeSetEntries.map(([name, nodeSet]) => (
              <li key={name}>
                <strong className="cluster-details__node-set-name">{name}</strong>
                <Content component="p" className="cluster-details__node-set-summary">
                  {t('{{hostType}}-{{count}} Nodes-{{version}}', {
                    replace: {
                      hostType: displayValue(nodeSet.baremetalInstanceType?.name),
                      count: nodeSet.size ?? '—',
                      version: displayValue(cluster.status?.nodeSets[name]?.observedVersion),
                    },
                  })}
                </Content>
              </li>
            ))}
          </ul>
        ) : (
          <Content component="p">{t('No node sets configured.')}</Content>
        )}
      </StackItem>
    </Stack>
  );
};

export default ClusterNodeSetsSection;
